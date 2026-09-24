// Package gateway serves AI proxy requests and the authenticated operator API.
package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/admin/login" && r.Method == http.MethodPost {
		s.login(w, r)
		return
	}
	if !s.authenticated(r) {
		http.Error(w, "administrator login required", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet && !sameOrigin(r) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.URL.Path == "/admin/logout" && r.Method == http.MethodPost:
		s.logout(w, r)
	case r.URL.Path == "/admin/session" && r.Method == http.MethodGet:
		writeJSON(w, map[string]bool{"authenticated": true})
	case r.URL.Path == "/admin/jev" && r.Method == http.MethodGet:
		settings, err := s.Store.Jev(r.Context())
		if err != nil {
			http.Error(w, "Jev 配置暂不可用", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, settings.public())
	case r.URL.Path == "/admin/jev" && r.Method == http.MethodPut:
		var input JevConfig
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&input) != nil {
			http.Error(w, "Jev 配置格式错误", http.StatusBadRequest)
			return
		}
		previous, err := s.Store.Jev(r.Context())
		if err != nil {
			http.Error(w, "Jev 配置暂不可用", http.StatusServiceUnavailable)
			return
		}
		input.BaseURL, input.Model = strings.TrimSpace(input.BaseURL), strings.TrimSpace(input.Model)
		if input.APIKey == "" {
			input.APIKey = previous.APIKey
		}
		if err := input.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := s.Store.UpdateJev(r.Context(), input)
		if err != nil {
			http.Error(w, "Jev 配置保存失败", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, saved.public())
	case r.URL.Path == "/admin/jev/test" && r.Method == http.MethodPost:
		var input struct {
			Text string `json:"text"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&input) != nil || strings.TrimSpace(input.Text) == "" {
			http.Error(w, "请输入测试文本", http.StatusBadRequest)
			return
		}
		settings, err := s.Store.Jev(r.Context())
		if err != nil {
			http.Error(w, "Jev 配置暂不可用", http.StatusServiceUnavailable)
			return
		}
		if err := settings.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.Timeout)
		defer cancel()
		started := time.Now()
		var scores []policy.Answer
		select {
		case s.Slots <- struct{}{}:
			if configured, ok := s.Classifier.(configuredClassifier); ok {
				scores, err = configured.CheckConfigured(ctx, input.Text, settings)
			} else {
				scores, err = s.Classifier.Check(ctx, input.Text)
			}
			<-s.Slots
		case <-ctx.Done():
			err = ctx.Err()
		}
		s.markClassifier(err, ctx.Err() != nil)
		if err != nil {
			http.Error(w, "Jev 测试失败，请检查地址、模型、密钥及服务状态", http.StatusBadGateway)
			return
		}
		if policy.ValidateAnswers(scores) != nil {
			http.Error(w, "Jev 返回的审核分数不完整或格式错误", http.StatusBadGateway)
			return
		}
		p, err := s.Store.Policy(r.Context())
		if err != nil {
			http.Error(w, "策略暂不可用", http.StatusServiceUnavailable)
			return
		}
		p.Enabled = true // A test may simulate rules while production review is off.
		var decision *policy.Decision
		if policy.Validate(p) == nil {
			result, err := policy.Evaluate(p, scores)
			if err != nil {
				http.Error(w, "Jev 返回的审核分数不完整或格式错误", http.StatusBadGateway)
				return
			}
			decision = &result
		}
		writeJSON(w, struct {
			Scores       []policy.Answer  `json:"scores"`
			Decision     *policy.Decision `json:"decision"`
			PolicyReady  bool             `json:"policy_ready"`
			ClassifierMS int64            `json:"classifier_ms"`
		}{scores, decision, decision != nil, time.Since(started).Milliseconds()})
	case r.URL.Path == "/admin/policy" && r.Method == http.MethodGet:
		p, err := s.Store.Policy(r.Context())
		if err != nil {
			http.Error(w, "policy unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, struct {
			policy.Policy
			Questions []policy.Question `json:"questions"`
		}{p, policy.Questions})
	case r.URL.Path == "/admin/policy" && r.Method == http.MethodPut:
		var next policy.Policy
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&next) != nil {
			http.Error(w, "invalid policy JSON", http.StatusBadRequest)
			return
		}
		if err := policy.Validate(next); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if next.Enabled {
			settings, err := s.Store.Jev(r.Context())
			if err != nil {
				http.Error(w, "Jev 配置暂不可用", http.StatusServiceUnavailable)
				return
			}
			if err := settings.validate(); err != nil {
				http.Error(w, "请先在设置中保存完整的 Jev 连接", http.StatusBadRequest)
				return
			}
		}
		updated, err := s.Store.UpdatePolicy(r.Context(), next.Version, next, "admin")
		if errors.Is(err, ErrConflict) {
			http.Error(w, "policy changed; reload before saving", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, "policy save failed", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, updated)
	case r.URL.Path == "/admin/overview" && r.Method == http.MethodGet:
		hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
		if hours < 1 || hours > 168 {
			hours = 24
		}
		result, err := s.Store.Overview(r.Context(), time.Now().Add(-time.Duration(hours)*time.Hour))
		if err != nil {
			http.Error(w, "statistics unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, result)
	case r.URL.Path == "/admin/events" && r.Method == http.MethodGet:
		q := r.URL.Query()
		hours, _ := strconv.Atoi(q.Get("hours"))
		if hours < 1 || hours > 720 {
			hours = 24
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		if offset < 0 {
			offset = 0
		}
		items, err := s.Store.Events(r.Context(), EventFilter{Since: time.Now().Add(-time.Duration(hours) * time.Hour), Kind: q.Get("kind"), Action: q.Get("action"), Search: q.Get("search"), Limit: 50, Offset: offset})
		if err != nil {
			http.Error(w, "events unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, items)
	case strings.HasPrefix(r.URL.Path, "/admin/events/") && r.Method == http.MethodGet:
		item, err := s.Store.Event(r.Context(), strings.TrimPrefix(r.URL.Path, "/admin/events/"))
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "event unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, item)
	case r.URL.Path == "/admin/policy-changes" && r.Method == http.MethodGet:
		items, err := s.Store.Changes(r.Context())
		if err != nil {
			http.Error(w, "changes unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, items)
	case r.URL.Path == "/admin/runtime" && r.Method == http.MethodGet:
		s.statusMu.Lock()
		state := s.classifierState
		if state == "" {
			state = "not_checked"
		}
		value := map[string]any{"classifier": state, "timeout_ms": s.Timeout.Milliseconds(), "concurrency": cap(s.Slots), "in_flight": len(s.Slots), "upstream_url": s.UpstreamURL, "last_checked_at": s.lastCheckedAt, "last_error_kind": s.lastErrorKind, "last_error_at": s.lastErrorAt}
		s.statusMu.Unlock()
		writeJSON(w, value)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	got, want := sha256.Sum256([]byte(input.Password)), sha256.Sum256([]byte(s.AdminPassword))
	if s.AdminPassword == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}
	token := newID()
	s.sessionMu.Lock()
	s.sessions[token] = time.Now().Add(12 * time.Hour)
	s.sessionMu.Unlock()
	// #nosec G124 -- Local HTTP is bound to loopback; HTTPS requests receive Secure cookies.
	http.SetCookie(w, &http.Cookie{Name: "sael_session", Value: token, Path: "/admin", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || strings.HasPrefix(r.Header.Get("Origin"), "https://"), Expires: time.Now().Add(12 * time.Hour)})
	writeJSON(w, map[string]bool{"authenticated": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie("sael_session")
	if cookie != nil {
		s.sessionMu.Lock()
		delete(s.sessions, cookie.Value)
		s.sessionMu.Unlock()
	}
	// #nosec G124 -- Matches the session cookie in local HTTP and HTTPS deployments.
	http.SetCookie(w, &http.Cookie{Name: "sael_session", Path: "/admin", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil || strings.HasPrefix(r.Header.Get("Origin"), "https://")})
	writeJSON(w, map[string]bool{"authenticated": false})
}

func (s *Server) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie("sael_session")
	if err != nil {
		return false
	}
	s.sessionMu.Lock()
	expires, ok := s.sessions[cookie.Value]
	if ok && time.Now().After(expires) {
		delete(s.sessions, cookie.Value)
		ok = false
	}
	s.sessionMu.Unlock()
	return ok
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
