package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/protocol"
)

type reviewAPIRequest struct {
	Input    string `json:"input"`
	Endpoint string `json:"endpoint,omitempty"`
}

// A separate admission gate keeps synchronous API calls from exhausting the CLI pool.
const reviewAPIMaxConcurrent = 64

func (s *Server) reviewAPI(w http.ResponseWriter, r *http.Request) {
	started := time.Now().UTC()
	p, err := s.Store.Policy(r.Context())
	if err != nil {
		writeReviewAPIError(w, 503, "policy_unavailable", "审核接口配置暂不可用")
		return
	}
	if !p.ReviewAPIEnabled {
		writeReviewAPIError(w, 404, "review_api_disabled", "HTTP 审查接口未启用")
		return
	}
	store, ok := s.Store.(ReviewAPIStore)
	if !ok {
		writeReviewAPIError(w, 503, "review_api_unavailable", "审核接口暂不可用")
		return
	}
	stat := ReviewAPIStat{Time: started, RequestSource: "review_api", Outcome: "error"}
	defer func() {
		stat.DurationMS = time.Since(started).Milliseconds()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := store.RecordReviewAPIStat(ctx, stat); err != nil {
			slog.Error("review API statistics write failed", "error", err)
		}
	}()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeReviewAPIError(w, 405, "method_not_allowed", "请使用 POST 请求")
		return
	}
	secret, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeReviewAPIError(w, 401, "invalid_api_key", "审核 API Key 无效")
		return
	}
	key, err := store.AuthenticateReviewAPIKey(r.Context(), secret)
	if err != nil {
		if errors.Is(err, ErrInvalidReviewAPIKey) {
			writeReviewAPIError(w, 401, "invalid_api_key", "审核 API Key 无效")
		} else {
			writeReviewAPIError(w, 503, "authentication_unavailable", "密钥验证暂不可用")
		}
		return
	}
	stat.APIKeyID = key.ID
	if s.reviewAPIActive.Add(1) > reviewAPIMaxConcurrent {
		s.reviewAPIActive.Add(-1)
		w.Header().Set("Retry-After", "1")
		writeReviewAPIError(w, 429, "review_api_busy", "审核请求过多，请稍后重试")
		return
	}
	defer s.reviewAPIActive.Add(-1)
	input, status, err := s.decodeReviewAPI(w, r)
	if err != nil {
		writeReviewAPIError(w, status, "invalid_input", err.Error())
		return
	}
	event := Event{ExecutionMode: policy.Blocking, ID: "sael_mod_" + newID(), Time: started, RequestSource: "review_api", ReviewAPIKeyID: key.ID, MaskedKey: key.Masked,
		Endpoint: "/v1/moderations", Protocol: input.Endpoint, Text: input.Input,
		InputChars: utf8.RuneCountInString(input.Input), ClientIP: s.clientIP(r), UserAgent: r.UserAgent(), ClientRequestID: r.Header.Get("X-Request-Id"), ContentType: r.Header.Get("Content-Type"), RequestBytes: r.ContentLength}
	event.RequestID = event.ID
	response, result, reviewed, status := s.evaluateReviewAPI(r.Context(), p, input, event)
	stat.CacheHit = result.CacheHit
	if status != 200 {
		writeReviewAPIError(w, status, "review_unavailable", reviewAPIStatusMessage(status))
		return
	}
	stat.Outcome = "allowed"
	if response.Flagged {
		stat.Outcome = "hit"
		stat.SceneID, stat.SceneName = reviewed.Decision.SceneID, reviewed.Decision.SceneName
		stat.Scenes = response.Scenes
		if response.Action == policy.Block {
			stat.Outcome = "blocked"
		}
		s.writeEvent(r.Context(), reviewed)
	}
	writeJSON(w, response)
}

func (s *Server) decodeReviewAPI(w http.ResponseWriter, r *http.Request) (reviewAPIRequest, int, error) {
	var input reviewAPIRequest
	if r.ContentLength > s.MaxBodyBytes {
		return input, 413, errors.New("请求体超过限制")
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&input)
	if err == nil {
		var extra any
		if next := dec.Decode(&extra); !errors.Is(next, io.EOF) {
			if next == nil {
				next = errors.New("multiple JSON values")
			}
			err = next
		}
	}
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			return input, 413, errors.New("请求体超过限制")
		}
		return input, 400, errors.New("请求必须是单个 JSON 对象，input 必须是文本")
	}
	if strings.TrimSpace(input.Input) == "" {
		return input, 400, errors.New("input 必须是非空文本")
	}
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	if input.Endpoint != "" && !protocol.Monitored(input.Endpoint) {
		return input, 400, errors.New("endpoint 无效")
	}
	return input, 200, nil
}

func (s *Server) evaluateReviewAPI(ctx context.Context, p policy.Policy, input reviewAPIRequest, event Event) (reviewAPIResponse, Count, Event, int) {
	// Omitted context means all active scenes; this modifies only the request snapshot.
	scenes := append([]policy.Scene(nil), p.Scenes...)
	for i := range scenes {
		if input.Endpoint == "" {
			scenes[i].Endpoints = nil
		}
		// The public review API has no model selector; evaluate against every model.
		scenes[i].Models = nil
	}
	p.Scenes, p.Enabled = scenes, true
	result, decision, reviewed := s.reviewWithEvent(ctx, event, p, Count{ID: event.ID, Time: event.Time, Protocol: input.Endpoint}, false)
	response := reviewAPIResponse{ID: event.ID, Object: "sael.moderation", Created: event.Time.Unix(), Flagged: decision.SceneID != "", Action: decision.Action,
		Scenes: reviewAPIScenes(p, decision, []Event{reviewed}), Scores: []ReviewAPIScore{}}
	for _, score := range reviewed.Scores {
		response.Scores = append(response.Scores, ReviewAPIScore{Question: score.Question, Value: score.Value})
	}
	switch result.Outcome {
	case "input_too_long":
		return response, result, reviewed, 200
	case "unreviewed", "client_canceled":
		return response, result, reviewed, 503
	}
	return response, result, reviewed, 200
}

func reviewAPIStatusMessage(status int) string {
	if status == 413 {
		return "input 超过审核上限"
	}
	return "审核服务暂不可用"
}

func reviewAPIScenes(p policy.Policy, decision policy.Decision, events []Event) []ReviewAPIScene {
	matched := map[string]policy.SceneTrace{}
	if len(events) > 0 {
		for _, trace := range events[len(events)-1].Trace {
			if trace.Status == "effective" {
				matched[trace.ID] = trace
			}
		}
	}
	result := []ReviewAPIScene{}
	for _, scene := range p.Scenes {
		trace, ok := matched[scene.ID]
		if !ok && scene.ID != decision.SceneID {
			continue
		}
		hits := []policy.Hit{}
		for _, condition := range trace.Conditions {
			if condition.Matched {
				hits = append(hits, policy.Hit{Question: condition.Question, Value: condition.Value, Threshold: condition.Threshold, RecordOnly: condition.RecordOnly})
			}
		}
		result = append(result, ReviewAPIScene{ID: scene.ID, Name: scene.Name, Matched: true, Hits: hits})
	}
	return result
}

func bearerToken(value string) (string, bool) {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	return parts[1], true
}

func writeReviewAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
