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
		Text          string          `json:"text"`
		Endpoint      string          `json:"endpoint"`
		Model         string          `json:"model"`
		Policy        *policy.Policy  `json:"policy,omitempty"`
		ComparePolicy *policy.Policy  `json:"compare_policy,omitempty"`
		Scores        []policy.Answer `json:"scores,omitempty"`
		Connection    *JevConfig      `json:"connection,omitempty"`
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
	required := policy.RequiredQuestions(p, input.Endpoint, input.Model)
	if len(p.Scenes) == 0 && input.ComparePolicy == nil {
		for _, question := range policy.Questions {
			required = append(required, question.Key)
		}
	}
	if input.ComparePolicy != nil {
		if err := policy.Validate(*input.ComparePolicy); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		needed := make(map[string]bool, len(required))
		for _, key := range required {
			needed[key] = true
		}
		for _, key := range policy.RequiredQuestions(*input.ComparePolicy, input.Endpoint, input.Model) {
			needed[key] = true
		}
		required = nil
		for _, question := range policy.Questions {
			if needed[question.Key] {
				required = append(required, question.Key)
			}
		}
	}
	scores := input.Scores
	var elapsed int64
	selectedResponse := false
	if scores == nil {
		if strings.TrimSpace(input.Text) == "" {
			http.Error(w, "请输入测试文本", http.StatusBadRequest)
			return
		}
		if len(required) == 0 {
			decision, trace, _ := policy.EvaluateDetailed(p, nil, input.Endpoint, input.Model)
			writeJSON(w, map[string]any{"scores": []policy.Answer{}, "decision": decision, "trace": trace, "policy_ready": true, "classifier_ms": 0})
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
		inputChars, overLimit := inputCharsOverLimit(input.Text, settings.inputLimit())
		if overLimit {
			writeJSON(w, map[string]any{"skipped": true, "reason": "classifier_input_too_long", "input_chars": inputChars, "input_limit_chars": settings.inputLimit(), "scores": []policy.Answer{}, "decision": policy.Decision{Action: policy.Allow, Hits: []policy.Hit{}}, "trace": []policy.SceneTrace{}, "policy_ready": len(p.Scenes) > 0, "classifier_ms": 0})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), settings.timeout())
		defer cancel()
		started := time.Now()
		if configured, ok := s.Classifier.(configuredClassifier); ok {
			selectedResponse = true
			scores, err = configured.CheckConfigured(ctx, input.Text, settings, required)
		} else {
			scores, err = s.Classifier.Check(ctx, input.Text)
		}
		elapsed = time.Since(started).Milliseconds()
		if err == nil {
			if selectedResponse {
				err = policy.ValidateAnswersFor(scores, required)
			} else {
				err = policy.ValidateAnswers(scores)
			}
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
	} else if _, _, err := policy.EvaluateDetailed(p, scores, input.Endpoint, input.Model); err != nil {
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
