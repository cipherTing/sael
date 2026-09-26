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
)

// sessionBlockKey isolates a session by the credential forwarded to the upstream.
// Credentials and raw session identifiers are never persisted as freeze keys.
func sessionBlockKey(r *http.Request, session string) string {
	credential := strings.TrimSpace(r.Header.Get("X-Api-Key"))
	if credential == "" {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			credential = parts[1]
		}
	}
	if credential == "" || session == "" {
		return ""
	}
	raw, _ := json.Marshal([2]string{credential, session})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Server) sessionBlocked(ctx context.Context, key string) bool {
	if key == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	active, err := s.Store.SessionBlockActive(ctx, key, time.Now().UTC())
	if err != nil {
		slog.Warn("session freeze lookup failed", "error", err)
	}
	return active
}

func (s *Server) rememberSessionBlock(ctx context.Context, key string, ttl time.Duration) {
	if key == "" || ttl <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.Store.PutSessionBlock(ctx, key, time.Now().UTC().Add(ttl)); err != nil {
		slog.Warn("session freeze write failed", "error", err)
	}
}

func requestSession(r *http.Request, bodySession string) string {
	for _, name := range []string{"session-id", "session_id", "X-Session-Id", "X-Claude-Code-Session-Id"} {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return bodySession
}
