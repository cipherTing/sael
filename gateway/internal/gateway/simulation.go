package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/protocol"
)

func (s *Server) testPolicy(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text       string          `json:"text"`
		Endpoint   string          `json:"endpoint"`
		Model      string          `json:"model"`
		Policy     *policy.Policy  `json:"policy,omitempty"`
		Scores     []policy.Answer `json:"scores,omitempty"`
		Connection *JevConfig      `json:"connection,omitempty"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil {
		http.Error(w, "测试参数格式错误", http.StatusBadRequest)
		return
	}
	if input.Endpoint == "" {
		input.Endpoint = "openai_chat"
	}
	if !protocol.Monitored(input.Endpoint) {
		http.Error(w, "请选择有效端点", http.StatusBadRequest)
		return
	}
	p, err := s.Store.Policy(r.Context())
	if err != nil {
		http.Error(w, "策略暂不可用", http.StatusServiceUnavailable)
		return
	}
	if input.Policy != nil {
		p = *input.Policy
	}
	p.Enabled = len(p.Scenes) > 0
	if err := policy.Validate(p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	scores := input.Scores
	var elapsed int64
	if scores == nil {
		if strings.TrimSpace(input.Text) == "" {
			http.Error(w, "请输入测试文本", http.StatusBadRequest)
			return
		}
		settings, err := s.Store.Jev(r.Context())
		if err != nil {
			http.Error(w, "Jev 配置暂不可用", http.StatusServiceUnavailable)
			return
		}
		if input.Connection != nil {
			next := *input.Connection
			if next.APIKey == "" {
				next.APIKey = settings.APIKey
			}
			settings = next
		}
		if err := settings.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		inputTokens, tokenErr := inputTokensOverLimit(input.Text, settings.inputLimit())
		if tokenErr == nil && inputTokens > settings.inputLimit() {
			writeJSON(w, map[string]any{"skipped": true, "reason": "classifier_input_too_long", "input_tokens_estimated": inputTokens, "input_limit_tokens": settings.inputLimit(), "scores": []policy.Answer{}, "decision": policy.Decision{Action: policy.Allow, Hits: []policy.Hit{}}, "trace": []policy.SceneTrace{}, "policy_ready": len(p.Scenes) > 0, "classifier_ms": 0})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), settings.timeout())
		defer cancel()
		started := time.Now()
		if configured, ok := s.Classifier.(configuredClassifier); ok {
			scores, err = configured.CheckConfigured(ctx, input.Text, settings)
		} else {
			scores, err = s.Classifier.Check(ctx, input.Text)
		}
		elapsed = time.Since(started).Milliseconds()
		if err == nil {
			err = policy.ValidateAnswers(scores)
			if err != nil {
				err = fmt.Errorf("%w: %w", ErrInvalidClassifierResponse, err)
			}
		}
		// Draft connection checks must not replace the saved connection's runtime state.
		if input.Connection == nil && !errors.Is(r.Context().Err(), context.Canceled) {
			s.markClassifier(err, errors.Is(ctx.Err(), context.DeadlineExceeded))
		}
		if err != nil {
			http.Error(w, "Jev 测试失败，请检查连接和返回结果", http.StatusBadGateway)
			return
		}
	} else if err := policy.ValidateAnswers(scores); err != nil {
		http.Error(w, "审核分数不完整或超出范围", http.StatusBadRequest)
		return
	}
	decision, trace, err := policy.EvaluateDetailed(p, scores, input.Endpoint, input.Model)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"scores": scores, "decision": decision, "trace": trace, "policy_ready": len(p.Scenes) > 0, "classifier_ms": elapsed})
}
