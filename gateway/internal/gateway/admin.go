// Package gateway serves AI proxy requests and the authenticated operator API.
package gateway

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/protocol"
)

type jevConfigInput struct {
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	APIKey         string `json:"api_key"`
	TimeoutMS      int    `json:"timeout_ms"`
	MaxInputTokens *int   `json:"max_input_tokens"`
}

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
	case r.URL.Path == "/admin/access" && r.Method == http.MethodGet:
		writeJSON(w, map[string]any{"ingress_listen": s.IngressAddress, "ingress_url": s.PublicIngressURL, "endpoints": protocol.Endpoints})
	case r.URL.Path == "/admin/analytics" && r.Method == http.MethodGet:
		f, err := analyticsFilter(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := s.Store.Analytics(r.Context(), f)
		if err != nil {
			http.Error(w, "统计数据暂不可用", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, result)
	case r.URL.Path == "/admin/upstream" && r.Method == http.MethodGet:
		config, err := s.Store.Upstream(r.Context())
		if err != nil {
			http.Error(w, "上游配置暂不可用", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, config.public())
	case r.URL.Path == "/admin/upstream" && r.Method == http.MethodPut:
		var input UpstreamConfig
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&input) != nil {
			http.Error(w, "上游配置格式错误", http.StatusBadRequest)
			return
		}
		input.BaseURL = strings.TrimSpace(input.BaseURL)
		if _, err := input.target(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := s.Store.UpdateUpstream(r.Context(), input)
		if err != nil {
			http.Error(w, "上游配置保存失败", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, saved.public())
	case r.URL.Path == "/admin/jev" && r.Method == http.MethodGet:
		settings, err := s.Store.Jev(r.Context())
		if err != nil {
			http.Error(w, "Jev 配置暂不可用", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, settings.public())
	case r.URL.Path == "/admin/jev" && r.Method == http.MethodPut:
		var raw jevConfigInput
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&raw) != nil {
			http.Error(w, "Jev 配置格式错误", http.StatusBadRequest)
			return
		}
		previous, err := s.Store.Jev(r.Context())
		if err != nil {
			http.Error(w, "Jev 配置暂不可用", http.StatusServiceUnavailable)
			return
		}
		input := JevConfig{BaseURL: strings.TrimSpace(raw.BaseURL), Model: strings.TrimSpace(raw.Model), APIKey: raw.APIKey, TimeoutMS: raw.TimeoutMS, MaxInputTokens: previous.inputLimit()}
		if raw.MaxInputTokens != nil {
			if *raw.MaxInputTokens <= 0 {
				http.Error(w, "送审上限必须是正整数", http.StatusBadRequest)
				return
			}
			input.MaxInputTokens = *raw.MaxInputTokens
		}
		if input.APIKey == "" {
			input.APIKey = previous.APIKey
		}
		if input.TimeoutMS == 0 {
			input.TimeoutMS = int(previous.timeout().Milliseconds())
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
	case (r.URL.Path == "/admin/jev/test" || r.URL.Path == "/admin/policy/test") && r.Method == http.MethodPost:
		s.testPolicy(w, r)
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
		since, until, rangeErr := overviewRange(r.URL.Query(), time.Now().UTC())
		if rangeErr != nil {
			http.Error(w, rangeErr.Error(), http.StatusBadRequest)
			return
		}
		result, err := s.Store.Overview(r.Context(), since, until)
		if err != nil {
			http.Error(w, "statistics unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, result)
	case r.URL.Path == "/admin/events" && r.Method == http.MethodGet:
		q := r.URL.Query()
		f, err := analyticsFilter(q)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		if offset < 0 {
			offset = 0
		}
		items, err := s.Store.Events(r.Context(), EventFilter{Since: f.Since, Until: f.Until, Endpoint: f.Endpoint, Model: f.Model, Scene: q.Get("scene"), ErrorKind: q.Get("error_kind"), Kind: q.Get("kind"), Action: q.Get("action"), Search: q.Get("search"), ClientIP: q.Get("client_ip"), SessionID: q.Get("session_id"), Limit: 50, Offset: offset})
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
		value := map[string]any{"classifier": state, "last_checked_at": s.lastCheckedAt, "last_error_kind": s.lastErrorKind, "last_error_at": s.lastErrorAt}
		s.statusMu.Unlock()
		writeJSON(w, value)
	default:
		http.NotFound(w, r)
	}
}

func overviewRange(query url.Values, now time.Time) (startTime, endTime time.Time, err error) {
	startText, endText := strings.TrimSpace(query.Get("start")), strings.TrimSpace(query.Get("end"))
	if startText == "" && endText == "" {
		if raw := query.Get("minutes"); raw != "" {
			minutes, err := strconv.Atoi(raw)
			if err != nil || minutes < 1 || minutes > 527040 {
				return time.Time{}, time.Time{}, errors.New("时间范围无效")
			}
			return now.Add(-time.Duration(minutes) * time.Minute), now, nil
		}
		hours, _ := strconv.Atoi(query.Get("hours"))
		if hours < 1 || hours > 720 {
			hours = 24
		}
		return now.Add(-time.Duration(hours) * time.Hour), now, nil
	}
	if startText == "" || endText == "" {
		return time.Time{}, time.Time{}, errors.New("开始和结束时间必须同时提供")
	}
	parse := func(value string) (time.Time, error) {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed, nil
		}
		return time.ParseInLocation("2006-01-02", value, time.UTC)
	}
	since, err := parse(startText)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("开始时间格式错误")
	}
	until, err := parse(endText)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("结束时间格式错误")
	}
	if !until.After(since) {
		return time.Time{}, time.Time{}, errors.New("结束时间必须晚于开始时间")
	}
	if until.Sub(since) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, errors.New("时间范围不能超过 366 天")
	}
	return since, until, nil
}

func analyticsFilter(q url.Values) (AnalyticsFilter, error) {
	since, until, err := overviewRange(q, time.Now().UTC())
	if err != nil {
		return AnalyticsFilter{}, err
	}
	endpoint := q.Get("endpoint")
	if endpoint != "" && !protocol.Monitored(endpoint) {
		return AnalyticsFilter{}, errors.New("端点无效")
	}
	window := until.Sub(since)
	since = since.Truncate(time.Minute)
	if until != until.Truncate(time.Minute) {
		until = until.Truncate(time.Minute).Add(time.Minute)
	}
	if q.Get("start") == "" && q.Get("end") == "" {
		since = until.Add(-window)
	}
	granularity := q.Get("granularity")
	switch granularity {
	case "", "1m", "5m", "1h", "1d":
	default:
		return AnalyticsFilter{}, errors.New("统计粒度无效")
	}
	zone := q.Get("timezone")
	if zone == "" {
		zone = "UTC"
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return AnalyticsFilter{}, errors.New("统计时区无效")
	}
	f := AnalyticsFilter{Since: since, Until: until, Endpoint: endpoint, Model: q.Get("model"), Granularity: granularity, Timezone: zone}
	return f, nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	if s.Security == nil {
		http.Error(w, "登录保护暂不可用", 503)
		return
	}
	retry, err := s.Security.LoginAttempt(r.Context(), s.clientIP(r))
	if err != nil {
		http.Error(w, "登录保护暂不可用", 503)
		return
	}
	if retry > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64((retry+time.Second-1)/time.Second), 10))
		http.Error(w, "登录尝试过于频繁，请稍后重试", 429)
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
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
