package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const maxTranscriptLookupKeys = 256

type transcriptPlan struct {
	exact           string
	keys            []string
	preLatest       string
	scope           string
	lookupTruncated bool
}

type sessionBlockPlan struct {
	explicit   string
	transcript transcriptPlan
}

// sessionBlockKey isolates a stable client session by the credential forwarded upstream.
// Credentials and raw session identifiers are never persisted as freeze keys.
func sessionBlockKey(r *http.Request, session string) string {
	credential := strings.TrimSpace(r.Header.Get("X-Api-Key"))
	if credential == "" {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			credential = parts[1]
		}
	}
	return hashSessionKey(credential, session)
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
	if credential == "" || clientIP == "" || userAgent == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("sael-session-scope:v1|credential=" + credential + "|ip=" + clientIP + "|ua=" + userAgent))
	return "scope:" + hex.EncodeToString(sum[:])
}

func normalizeUserAgent(value string) string {
	value = sanitizeSessionID(value)
	if value == "" {
		return ""
	}
	parts := strings.Fields(value)
	for i, part := range parts {
		if slash := strings.LastIndexByte(part, '/'); slash > 0 {
			parts[i] = part[:slash]
		}
	}
	return strings.Join(parts, " ")
}

func sessionPlanForRequest(r *http.Request, credential, clientIP string, body []byte, protocolName string) sessionBlockPlan {
	plan := sessionBlockPlan{}
	session := requestSession(r, bodySessionID(protocolName, body))
	plan.explicit = hashSessionKey(credential, session)
	plan.transcript = transcriptBlockPlan(credential, protocolName, body)
	plan.transcript.scope = sessionScopeKey(credential, clientIP, r.UserAgent())
	return plan
}

func transcriptBlockPlan(credential, protocolName string, body []byte) transcriptPlan {
	root, ok := decodeObject(body)
	if !ok || strings.TrimSpace(credential) == "" {
		return transcriptPlan{}
	}
	seed := "sael-transcript:v1|credential=" + strings.TrimSpace(credential) + "|protocol=" + protocolName
	if instructions, ok := root["instructions"]; ok && strings.TrimSpace(string(instructions)) != "" {
		seed += "|instructions=" + canonicalJSON(instructions)
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

func (s *Server) sessionBlocked(ctx context.Context, plan sessionBlockPlan) (string, string) {
	if plan.explicit != "" && s.sessionBlockActive(ctx, plan.explicit) {
		return plan.explicit, "explicit"
	}
	transcript := plan.transcript
	if transcript.scope == "" || len(transcript.keys) == 0 || !s.sessionBlockActive(ctx, transcript.scope) {
		return "", ""
	}
	if transcript.lookupTruncated {
		return "transcript_lookup_limit_exceeded", "history"
	}
	for _, key := range transcript.keys {
		if s.sessionBlockActive(ctx, key) {
			return key, "history"
		}
	}
	return "", ""
}

func (s *Server) sessionBlockActive(ctx context.Context, key string) bool {
	if key == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	active, err := s.Store.SessionBlockActive(ctx, key, time.Now().UTC())
	if err != nil {
		slog.Warn("session freeze lookup failed", "error", err)
		return false
	}
	return active
}

func (s *Server) rememberSessionBlock(ctx context.Context, plan sessionBlockPlan, ttl time.Duration) {
	keys := make([]string, 0, 4)
	if plan.explicit != "" {
		keys = append(keys, plan.explicit)
	}
	if plan.transcript.exact != "" {
		keys = append(keys, plan.transcript.exact)
	}
	if plan.transcript.preLatest != "" && plan.transcript.preLatest != plan.transcript.exact {
		keys = append(keys, plan.transcript.preLatest)
	}
	if plan.transcript.scope != "" {
		// The scope marker is written only after all exact keys succeed. This is
		// the same ordering used by sub2api: a scope without its exact blocks
		// would turn a storage partial failure into a broad overflow block.
	}
	scope := plan.transcript.scope
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
	if scope != "" && !writeFailed {
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
