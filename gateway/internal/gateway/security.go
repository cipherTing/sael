package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/cipherTing/sael/gateway/internal/protocol"
)

// SecurityStore keeps admission state shared across gateway instances.
type SecurityStore interface {
	LoginAttempt(context.Context, string) (time.Duration, error)
	TrustedKey(context.Context, string, time.Duration) (bool, error)
	RememberKey(context.Context, string, time.Duration) error
	ForgetKey(context.Context, string) error
}

type forwardStateKey struct{}
type forwardState struct {
	config  UpstreamConfig
	key     string
	idle    time.Duration
	trusted bool
}

// credentialFingerprint binds the forwarded credential to its destination.
// Match the relay auth convention: Bearer first, then X-Api-Key.
func credentialFingerprint(r *http.Request, target string) string {
	bearer := ""
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		bearer = parts[1]
	}
	if bearer == "" {
		bearer = strings.TrimSpace(r.Header.Get("X-Api-Key"))
	}
	if bearer == "" {
		return ""
	}
	raw, _ := json.Marshal([2]string{target, bearer})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Server) prepareForward(r *http.Request, idle time.Duration) (*http.Request, *forwardState, error) {
	if state, ok := r.Context().Value(forwardStateKey{}).(*forwardState); ok {
		return r, state, nil
	}
	cfg, err := s.Store.Upstream(r.Context())
	if err != nil {
		return r, nil, err
	}
	state := &forwardState{config: cfg, key: credentialFingerprint(r, cfg.BaseURL), idle: idle}
	if state.key != "" {
		if s.Security == nil {
			return r, nil, errSecurityUnavailable
		}
		state.trusted, err = s.Security.TrustedKey(r.Context(), state.key, idle)
		if err != nil {
			return r, nil, err
		}
	}
	return r.WithContext(context.WithValue(r.Context(), forwardStateKey{}, state)), state, nil
}

// observeCredential only learns from successful monitored business endpoints.
// It never consumes or changes the response, including long-lived SSE streams.
func (s *Server) observeCredential(resp *http.Response) error {
	state, ok := resp.Request.Context().Value(forwardStateKey{}).(*forwardState)
	if !ok || state.key == "" || resp.Request.Method != "POST" || !protocol.Monitored(protocol.Name(resp.Request.URL.Path)) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(resp.Request.Context()), time.Second)
	defer cancel()
	var err error
	if resp.StatusCode == http.StatusUnauthorized {
		err = s.Security.ForgetKey(ctx, state.key)
	} else if !state.trusted && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if contentType == "application/json" || contentType == "text/event-stream" {
			err = s.Security.RememberKey(ctx, state.key, state.idle)
		}
	}
	if err != nil {
		slog.Warn("credential state update failed", "error", err)
	}
	return nil
}
