package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxTranscriptLookupKeys = 256

var (
	sessionUserAgentProductPattern = regexp.MustCompile(`([A-Za-z0-9._-]+)/[A-Za-z0-9._-]+`)
	sessionUserAgentVersionPattern = regexp.MustCompile(`\bv?\d+(?:\.\d+){1,3}\b`)
)

type transcriptPlan struct {
	exact           string
	keys            []string
	preLatest       string
	scope           string
	lookupTruncated bool
}

type sessionBlockPlan struct {
	sessionID  string
	explicit   string
	transcript transcriptPlan
	derive     func() transcriptPlan
}

func hashSessionKey(credential, session string) string {
	credential, session = strings.TrimSpace(credential), sanitizeSessionID(session)
	if credential == "" || session == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("sael-session:v1|credential=" + credential + "|session=" + session))
	return hex.EncodeToString(sum[:])
}

func sessionScopeKey(credential, clientIP, userAgent string) string {
	credential = strings.TrimSpace(credential)
	clientIP = strings.TrimSpace(clientIP)
	userAgent = normalizeUserAgent(userAgent)
	if credential == "" || clientIP == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("sael-session-scope:v1|credential=" + credential + "|ip=" + clientIP + "|ua=" + userAgent))
	return "scope:" + hex.EncodeToString(sum[:])
}

func normalizeUserAgent(value string) string {
	products := sessionUserAgentProductPattern.FindAllStringSubmatch(value, -1)
	if len(products) == 0 {
		value = strings.ToLower(strings.Join(strings.Fields(value), " "))
		return strings.Join(strings.Fields(sessionUserAgentVersionPattern.ReplaceAllString(value, "")), " ")
	}
	names := make([]string, 0, len(products))
	for _, product := range products {
		names = append(names, strings.ToLower(product[1]))
	}
	slices.Sort(names)
	return strings.Join(slices.Compact(names), "+")
}

func sessionPlanForRequest(r *http.Request, credential, clientIP string, body []byte, protocolName string) sessionBlockPlan {
	plan := sessionBlockPlan{}
	session := requestSession(r, "")
	if session == "" {
		session = bodySessionID(protocolName, body)
	}
	plan.sessionID = session
	plan.explicit = hashSessionKey(credential, session)
	plan.transcript.scope = sessionScopeKey(credential, clientIP, r.UserAgent())
	plan.derive = sync.OnceValue(func() transcriptPlan {
		transcript := transcriptBlockPlan(credential, protocolName, body)
		transcript.scope = plan.transcript.scope
		return transcript
	})
	return plan
}

func (p sessionBlockPlan) history() transcriptPlan {
	if p.derive != nil {
		return p.derive()
	}
	return p.transcript
}

func transcriptBlockPlan(credential, protocolName string, body []byte) transcriptPlan {
	root, ok := decodeObject(body)
	if !ok || strings.TrimSpace(credential) == "" {
		return transcriptPlan{}
	}
	seed := "sael-transcript:v1|credential=" + strings.TrimSpace(credential) + "|protocol=" + protocolName
	if instructions, ok := root["instructions"]; ok {
		var text *string
		if json.Unmarshal(instructions, &text) != nil || text == nil || strings.TrimSpace(*text) != "" {
			seed += "|instructions=" + canonicalJSON(instructions)
		}
	}
	sequence, ok := transcriptSequence(protocolName, root)
	if !ok || len(sequence) == 0 {
		return transcriptPlan{}
	}
	h := sha256.New()
	_, _ = h.Write([]byte(seed))
	plan := transcriptPlan{keys: make([]string, 0, minInt(len(sequence), maxTranscriptLookupKeys))}
	hasModelItem := false
	last := ""
	for _, item := range sequence {
		canonical := canonicalJSON(item)
		if strings.TrimSpace(canonical) == "" {
			continue
		}
		if startsUserTurn(item) && hasModelItem && last != "" {
			plan.preLatest = last
		}
		_, _ = h.Write([]byte("|item=" + canonical))
		last = hex.EncodeToString(h.Sum(nil))
		if len(plan.keys) < maxTranscriptLookupKeys {
			plan.keys = append(plan.keys, last)
		} else {
			plan.lookupTruncated = true
			copy(plan.keys, plan.keys[1:])
			plan.keys[len(plan.keys)-1] = last
		}
		if isModelGenerated(item) {
			hasModelItem = true
		}
	}
	if len(plan.keys) > 0 {
		plan.exact = plan.keys[len(plan.keys)-1]
	}
	return plan
}

func transcriptSequence(protocolName string, root map[string]json.RawMessage) ([]json.RawMessage, bool) {
	field := "messages"
	if protocolName == "openai_responses" {
		field = "input"
	}
	var values []json.RawMessage
	if raw, ok := root[field]; ok && json.Unmarshal(raw, &values) == nil {
		return values, true
	}
	if protocolName == "openai_responses" {
		var text string
		if raw, ok := root[field]; ok && json.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) != "" {
			return []json.RawMessage{raw}, true
		}
	}
	return nil, false
}

func bodySessionID(protocolName string, body []byte) string {
	root, ok := decodeObject(body)
	if !ok {
		return ""
	}
	for _, key := range []string{"conversation_id", "prompt_cache_key"} {
		if raw, ok := root[key]; ok {
			var value string
			if json.Unmarshal(raw, &value) == nil {
				if value = sanitizeSessionID(value); value != "" {
					return value
				}
			}
		}
	}
	if raw, ok := root["conversation"]; ok {
		var id string
		if json.Unmarshal(raw, &id) == nil {
			return sanitizeSessionID(id)
		}
		var conversation struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &conversation) == nil {
			if value := sanitizeSessionID(conversation.ID); value != "" {
				return value
			}
		}
	}
	if protocolName == "anthropic" {
		if raw, ok := root["metadata"]; ok {
			var metadata struct {
				UserID string `json:"user_id"`
			}
			if json.Unmarshal(raw, &metadata) == nil {
				var identity struct {
					SessionID string `json:"session_id"`
				}
				if json.Unmarshal([]byte(metadata.UserID), &identity) == nil {
					return sanitizeSessionID(identity.SessionID)
				}
			}
		}
	}
	return ""
}

func decodeObject(body []byte) (map[string]json.RawMessage, bool) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return nil, false
	}
	var typ string
	_ = json.Unmarshal(root["type"], &typ)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(typ)), "response.") {
		var nested map[string]json.RawMessage
		if raw, ok := root["response"]; ok && json.Unmarshal(raw, &nested) == nil && nested != nil {
			return nested, true
		}
	}
	return root, true
}

func canonicalJSON(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(normalized)
}

func startsUserTurn(raw json.RawMessage) bool {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil {
		return false
	}
	var role, typ string
	_ = json.Unmarshal(item["role"], &role)
	_ = json.Unmarshal(item["type"], &typ)
	if strings.EqualFold(strings.TrimSpace(role), "user") {
		var content []json.RawMessage
		if raw, ok := item["content"]; ok && json.Unmarshal(raw, &content) == nil {
			for _, part := range content {
				var block map[string]json.RawMessage
				if json.Unmarshal(part, &block) != nil {
					return true
				}
				var blockType string
				_ = json.Unmarshal(block["type"], &blockType)
				switch strings.ToLower(strings.TrimSpace(blockType)) {
				case "tool_result", "function_call_output", "custom_tool_call_output", "computer_call_output":
					continue
				default:
					return true
				}
			}
			return false
		}
		return true
	}
	return strings.EqualFold(strings.TrimSpace(typ), "input_text")
}

func isModelGenerated(raw json.RawMessage) bool {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil {
		return false
	}
	var role, typ string
	_ = json.Unmarshal(item["role"], &role)
	_ = json.Unmarshal(item["type"], &typ)
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant", "model":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "output_text", "function_call", "tool_call", "custom_tool_call", "computer_call":
		return true
	default:
		return false
	}
}

func sanitizeSessionID(raw string) string {
	if !utf8.ValidString(raw) {
		return ""
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	count := 0
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return ""
		}
		count++
		if count > 255 {
			return ""
		}
	}
	return trimmed
}

func (s *Server) sessionBlocked(ctx context.Context, plan sessionBlockPlan) (key, source string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if plan.explicit != "" {
		key, err := s.Store.FindSessionBlock(ctx, []string{plan.explicit}, time.Now().UTC())
		if err != nil {
			slog.Warn("explicit session freeze lookup failed", "error", err)
			return "", ""
		}
		if key != "" {
			return key, "explicit"
		}
	}
	if plan.transcript.scope == "" {
		return "", ""
	}
	active, err := s.Store.SessionBlockActive(ctx, plan.transcript.scope, time.Now().UTC())
	if err != nil {
		slog.Warn("session freeze scope lookup failed", "error", err)
		return "", ""
	}
	if !active {
		return "", ""
	}
	transcript := plan.history()
	if transcript.lookupTruncated {
		return "transcript_lookup_limit_exceeded", "history"
	}
	key, err = s.Store.FindSessionBlock(ctx, transcript.keys, time.Now().UTC())
	if err != nil {
		slog.Warn("session freeze batch lookup failed", "error", err)
		return "", ""
	}
	if key != "" {
		return key, "history"
	}
	return "", ""
}

func (s *Server) rememberSessionBlock(ctx context.Context, plan sessionBlockPlan, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	transcript := plan.history()
	keys := make([]string, 0, 4)
	if plan.explicit != "" {
		keys = append(keys, plan.explicit)
	}
	if transcript.exact != "" {
		keys = append(keys, transcript.exact)
	}
	if transcript.preLatest != "" && transcript.preLatest != transcript.exact {
		keys = append(keys, transcript.preLatest)
	}
	// Write the scope marker only after all exact keys succeed. A scope without
	// its exact blocks could turn a storage failure into a broad overflow block.
	scope := transcript.scope
	writeFailed := false
	for _, key := range keys {
		if key == "" || ttl <= 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		if err := s.Store.PutSessionBlock(ctx, key, time.Now().UTC().Add(ttl)); err != nil {
			slog.Warn("session freeze write failed", "error", err)
			writeFailed = true
		}
		cancel()
	}
	if scope != "" && transcript.exact != "" && !writeFailed {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		if err := s.Store.PutSessionBlock(ctx, scope, time.Now().UTC().Add(ttl)); err != nil {
			slog.Warn("session freeze scope write failed", "error", err)
		}
		cancel()
	}
}

func requestSession(r *http.Request, bodySession string) string {
	for _, name := range []string{"session-id", "session_id", "X-Session-Id", "X-Claude-Code-Session-Id", "conversation_id", "X-Session-Affinity", "X-OpenCode-Session", "X-Conversation-ID"} {
		if value := sanitizeSessionID(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return sanitizeSessionID(bodySession)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
