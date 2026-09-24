package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/protocol"
)

var (
	// ErrConflict means a policy update used an outdated version.
	ErrConflict = errors.New("policy version conflict")
	// ErrNotFound means the requested stored record does not exist.
	ErrNotFound = errors.New("not found")
)

// Classifier returns the eleven measurements for one current user text.
type Classifier interface {
	Check(context.Context, string) ([]policy.Answer, error)
}

type configuredClassifier interface {
	CheckConfigured(context.Context, string, JevConfig) ([]policy.Answer, error)
}

// Event records one hit or classifier failure with its policy version.
type Event struct {
	ID            string          `json:"id"`
	Time          time.Time       `json:"time"`
	Kind          string          `json:"kind"`
	RequestID     string          `json:"request_id"`
	Protocol      string          `json:"protocol"`
	Endpoint      string          `json:"endpoint"`
	Model         string          `json:"model"`
	Stream        bool            `json:"stream"`
	HasNonText    bool            `json:"has_non_text_input"`
	TextPreview   string          `json:"text_preview"`
	Scores        []policy.Answer `json:"scores,omitempty"`
	Decision      policy.Decision `json:"decision"`
	PolicyVersion int64           `json:"policy_version"`
	ClassifierMS  int64           `json:"classifier_ms"`
	ErrorKind     string          `json:"error_kind,omitempty"`
}

// Count classifies one incoming request for minute aggregation.
type Count struct {
	ID               string    `json:"id"`
	Time             time.Time `json:"time"`
	Protocol         string    `json:"protocol"`
	Model            string    `json:"model"`
	Outcome          string    `json:"outcome"`
	ClassifierSample bool      `json:"classifier_sample"`
	ClassifierMS     int64     `json:"classifier_ms"`
}

// TrendPoint is one minute and outcome in the overview series.
type TrendPoint struct {
	Time              time.Time `json:"time"`
	Outcome           string    `json:"outcome"`
	Count             int64     `json:"count"`
	ClassifierSumMS   int64     `json:"classifier_sum_ms"`
	ClassifierSamples int64     `json:"classifier_samples"`
}

// NamedCount is an aggregated scene or question count.
type NamedCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// Overview summarizes traffic and outcomes for a time window.
type Overview struct {
	Since           time.Time    `json:"since"`
	UpdatedAt       time.Time    `json:"updated_at"`
	Total           int64        `json:"total"`
	Checked         int64        `json:"checked"`
	Hits            int64        `json:"hits"`
	Blocked         int64        `json:"blocked"`
	Unreviewed      int64        `json:"unreviewed"`
	NoText          int64        `json:"no_text"`
	Disabled        int64        `json:"disabled"`
	ClassifierAvgMS int64        `json:"classifier_avg_ms"`
	Trend           []TrendPoint `json:"trend"`
	Scenes          []NamedCount `json:"scenes"`
	Questions       []NamedCount `json:"questions"`
}

// EventFilter limits and searches the event list.
type EventFilter struct {
	Since                time.Time
	Kind, Action, Search string
	Limit, Offset        int
}

// PolicyChange keeps the policy before and after a saved update.
type PolicyChange struct {
	Time    time.Time     `json:"time"`
	Version int64         `json:"version"`
	Actor   string        `json:"actor"`
	Before  policy.Policy `json:"before"`
	After   policy.Policy `json:"after"`
}

// Store is the persistence boundary used by proxy and admin handlers.
type Store interface {
	Policy(context.Context) (policy.Policy, error)
	Jev(context.Context) (JevConfig, error)
	UpdateJev(context.Context, JevConfig) (JevConfig, error)
	Upstream(context.Context) (UpstreamConfig, error)
	UpdateUpstream(context.Context, UpstreamConfig) (UpstreamConfig, error)
	UpdatePolicy(context.Context, int64, policy.Policy, string) (policy.Policy, error)
	WriteEvent(context.Context, Event) error
	Increment(context.Context, Count) error
	Overview(context.Context, time.Time, time.Time) (Overview, error)
	Events(context.Context, EventFilter) ([]Event, error)
	Event(context.Context, string) (Event, error)
	Changes(context.Context) ([]PolicyChange, error)
}

// Server handles supported AI requests and authenticated admin routes.
type Server struct {
	Store           Store
	Classifier      Classifier
	AdminPassword   string
	Timeout         time.Duration
	Slots           chan struct{}
	sessionMu       sync.Mutex
	sessions        map[string]time.Time
	statusMu        sync.Mutex
	classifierState string
	lastCheckedAt   time.Time
	lastErrorKind   string
	lastErrorAt     time.Time
}

// New wires the saved configuration and classifier into an HTTP server.
func New(store Store, classifier Classifier, adminPassword string) *Server {
	return &Server{Store: store, Classifier: classifier, AdminPassword: adminPassword, Timeout: 5 * time.Second, Slots: make(chan struct{}, 32), sessions: make(map[string]time.Time)}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		s.admin(w, r)
		return
	}
	if r.Method == http.MethodPost && protocol.Supported(r.URL.Path) {
		s.proxyRequest(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) proxyRequest(w http.ResponseWriter, r *http.Request) {
	requestID := newID()
	w.Header().Set("X-Request-Id", requestID)
	count := Count{ID: requestID, Time: time.Now().UTC(), Protocol: protocol.Name(r.URL.Path), Outcome: "request_error"}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer cancel()
		if err := s.Store.Increment(ctx, count); err != nil {
			slog.Error("record count failed", "error", err)
		}
	}()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "unable to read request", http.StatusBadRequest)
		return
	}
	meta, err := protocol.Extract(r.URL.Path, body)
	if err != nil {
		count.Outcome = "invalid_json"
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	count.Model = meta.Model
	count.Outcome = "gateway_error"
	p, err := s.Store.Policy(r.Context())
	if err != nil {
		slog.Error("load policy failed", "error", err)
		http.Error(w, "policy unavailable", http.StatusServiceUnavailable)
		return
	}
	forward := func(outcome string) {
		config, err := s.Store.Upstream(r.Context())
		if err != nil {
			http.Error(w, "upstream configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		target, err := config.target()
		if err != nil {
			http.Error(w, "upstream is not configured", http.StatusServiceUnavailable)
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.SetXForwarded()
			},
			FlushInterval: -1,
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, "upstream unavailable", http.StatusBadGateway)
			},
		}
		count.Outcome = outcome
		proxy.ServeHTTP(w, r)
	}
	if !p.Enabled {
		forward("disabled")
		return
	}
	if meta.Text == "" {
		forward("no_text")
		return
	}
	config, configErr := s.Store.Jev(r.Context())
	timeout := s.Timeout
	if configErr == nil {
		timeout = config.timeout()
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	started := time.Now()
	var scores []policy.Answer
	select {
	case s.Slots <- struct{}{}:
		if configErr != nil {
			err = configErr
		} else if configured, ok := s.Classifier.(configuredClassifier); ok {
			if err = config.validate(); err == nil {
				scores, err = configured.CheckConfigured(ctx, meta.Text, config)
			}
		} else {
			scores, err = s.Classifier.Check(ctx, meta.Text)
		}
		<-s.Slots
	case <-ctx.Done():
		err = ctx.Err()
	}
	decision := policy.Decision{Action: policy.Allow}
	if err == nil {
		decision, err = policy.Evaluate(p, scores)
	}
	s.markClassifier(err, ctx.Err() != nil)
	count.ClassifierSample = true
	count.ClassifierMS = time.Since(started).Milliseconds()
	event := Event{ID: newID(), Time: time.Now().UTC(), RequestID: requestID,
		Protocol: meta.Protocol, Endpoint: r.URL.Path, Model: meta.Model, Stream: meta.Stream,
		HasNonText: meta.HasNonText, PolicyVersion: p.Version, ClassifierMS: time.Since(started).Milliseconds(),
		Scores: scores, Decision: decision}
	if p.PreviewChars != nil {
		event.TextPreview = preview(meta.Text, *p.PreviewChars)
	}
	if err != nil {
		event.Kind, event.ErrorKind = "failure", "classifier_unavailable"
		if ctx.Err() != nil {
			event.ErrorKind = "classifier_timeout"
		}
		s.writeEvent(r.Context(), event)
		forward("unreviewed")
		return
	}
	if len(decision.Hits) == 0 {
		forward("clean")
		return
	}
	event.Kind = "hit"
	s.writeEvent(r.Context(), event)
	if decision.Action == policy.Block {
		count.Outcome = "blocked"
		writeBlock(w, meta.Protocol, requestID)
		return
	}
	forward("hit_allowed")
}

func (s *Server) markClassifier(err error, timedOut bool) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.lastCheckedAt = time.Now().UTC()
	if err == nil {
		s.classifierState = "ok"
		return
	}
	s.classifierState = "error"
	s.lastErrorAt = s.lastCheckedAt
	s.lastErrorKind = "classifier_unavailable"
	if timedOut {
		s.lastErrorKind = "classifier_timeout"
	}
}

func (s *Server) writeEvent(ctx context.Context, e Event) {
	if err := s.Store.WriteEvent(ctx, e); err != nil {
		slog.Error("record event failed", "error", err, "request_id", e.RequestID)
	}
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

func preview(text string, length int) string {
	if length <= 0 {
		return ""
	}
	text = emailPattern.ReplaceAllString(text, "[已隐藏]")
	text = keyPattern.ReplaceAllString(text, "[已隐藏]")
	text = bearerPattern.ReplaceAllString(text, "[已隐藏]")
	r := []rune(strings.TrimSpace(text))
	if len(r) > length {
		r = r[:length]
	}
	return string(r)
}

var (
	emailPattern  = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	keyPattern    = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`)
	bearerPattern = regexp.MustCompile(`(?i)Bearer\s+\S+`)
)

func writeBlock(w http.ResponseWriter, protocolName, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	message := "Request blocked by gateway policy"
	var result any
	switch protocolName {
	case "anthropic":
		result = map[string]any{"type": "error", "error": map[string]any{"type": "permission_error", "message": message}}
	case "gemini":
		result = map[string]any{"error": map[string]any{"code": 403, "status": "PERMISSION_DENIED", "message": message}}
	default:
		result = map[string]any{"error": map[string]any{"type": "content_policy_violation", "message": message, "request_id": requestID}}
	}
	_ = json.NewEncoder(w).Encode(result)
}
