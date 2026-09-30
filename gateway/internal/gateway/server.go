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
	"mime"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/privacy"
	"github.com/cipherTing/sael/gateway/internal/protocol"
)

var (
	// ErrInvalidPolicy means a configuration update failed validation.
	ErrInvalidPolicy = errors.New("invalid policy update")
	// ErrNotFound means the requested stored record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalidClassifierResponse means the returned measurements cannot be evaluated.
	ErrInvalidClassifierResponse = errors.New("invalid classifier response")
)

// Classifier returns the eleven measurements for one current user text.
type Classifier interface {
	Check(context.Context, string) ([]policy.Answer, error)
}

type configuredClassifier interface {
	CheckConfigured(context.Context, string, JevConfig, []string) ([]policy.Answer, error)
}

// Event records one hit or classifier failure.
type Event struct {
	ContentType     string              `json:"content_type,omitempty"`
	RequestBytes    int64               `json:"request_bytes,omitempty"`
	ReviewSource    string              `json:"review_source,omitempty"`
	CredentialID    string              `json:"credential_id,omitempty"`
	MaskedKey       string              `json:"masked_key,omitempty"`
	EndpointGroup   string              `json:"endpoint_group,omitempty"`
	ImageOperation  string              `json:"image_operation,omitempty"`
	ClientIP        string              `json:"client_ip,omitempty"`
	UserAgent       string              `json:"user_agent,omitempty"`
	SessionID       string              `json:"session_id,omitempty"`
	SessionSource   string              `json:"session_source,omitempty"`
	SessionRef      string              `json:"session_ref,omitempty"`
	ClientRequestID string              `json:"client_request_id,omitempty"`
	Parameters      protocol.Parameters `json:"parameters"`
	ID              string              `json:"id"`
	Time            time.Time           `json:"time"`
	Kind            string              `json:"kind"`
	RequestID       string              `json:"request_id"`
	Protocol        string              `json:"protocol"`
	Endpoint        string              `json:"endpoint"`
	Model           string              `json:"model"`
	Stream          bool                `json:"stream"`
	HasNonText      bool                `json:"has_non_text_input"`
	TextPreview     string              `json:"text_preview"`
	TextAvailable   bool                `json:"text_available"`
	TextChars       int                 `json:"text_chars"`
	Text            string              `json:"text,omitempty"`
	Trace           []policy.SceneTrace `json:"trace,omitempty"`
	Scores          []policy.Answer     `json:"scores,omitempty"`
	Decision        policy.Decision     `json:"decision"`
	ClassifierMS    int64               `json:"classifier_ms,omitempty"`
	InputChars      int                 `json:"input_chars,omitempty"`
	InputTokens     int                 `json:"input_tokens_estimated,omitempty"`
	JevInputLimit   int                 `json:"jev_input_limit,omitempty"`
	ErrorKind       string              `json:"error_kind,omitempty"`
}

// Count classifies one incoming request for minute aggregation.
type Count struct {
	// UserInput is a runtime gate for ingress statistics. It is deliberately
	// not persisted because the database only stores counts that passed this
	// gate, and old rows must keep their original meaning.
	UserInput        bool            `json:"-"`
	CacheLookup      bool            `json:"cache_lookup,omitempty"`
	CacheHit         bool            `json:"cache_hit,omitempty"`
	ID               string          `json:"id"`
	Time             time.Time       `json:"time"`
	Protocol         string          `json:"protocol"`
	Model            string          `json:"model"`
	Outcome          string          `json:"outcome"`
	ClassifierSample bool            `json:"classifier_sample"`
	ClassifierMS     int64           `json:"classifier_ms"`
	JevMS            int64           `json:"jev_ms"`
	Scores           []policy.Answer `json:"scores,omitempty"`
	SceneMatches     []SceneMatch    `json:"scene_matches,omitempty"`
	ErrorKind        string          `json:"error_kind,omitempty"`
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
	Since                             time.Time
	Until                             time.Time
	Kind, Action, Search              string
	Endpoint, Model, Scene, ErrorKind string
	ClientIP, SessionID, CredentialID string
	Limit, Offset                     int
}

// PolicyChange keeps the policy before and after a saved update.
type PolicyChange struct {
	Time   time.Time     `json:"time"`
	Actor  string        `json:"actor"`
	Before policy.Policy `json:"before"`
	After  policy.Policy `json:"after"`
}

// Store is the persistence boundary used by proxy and admin handlers.
type Store interface {
	Policy(context.Context) (policy.Policy, error)
	Jev(context.Context) (JevConfig, error)
	UpdateJev(context.Context, JevConfig) (JevConfig, error)
	Upstream(context.Context) (UpstreamConfig, error)
	UpdateUpstream(context.Context, UpstreamConfig) (UpstreamConfig, error)
	UpdatePolicy(context.Context, PolicyUpdate, string) (policy.Policy, error)
	WriteEvent(context.Context, Event) error
	Increment(context.Context, Count) error
	Overview(context.Context, time.Time, time.Time) (Overview, error)
	Analytics(context.Context, AnalyticsFilter) (Analytics, error)
	Events(context.Context, EventFilter) ([]Event, error)
	Event(context.Context, string) (Event, error)
	Changes(context.Context) ([]PolicyChange, error)
	PutSessionBlock(context.Context, string, time.Time) error
	SessionBlockActive(context.Context, string, time.Time) (bool, error)
}

type proxySnapshot struct {
	baseURL string
	handler *httputil.ReverseProxy
}

type relayBuffers struct{ sync.Pool }

func (b *relayBuffers) Get() []byte {
	if v := b.Pool.Get(); v != nil {
		return v.(*[32 * 1024]byte)[:]
	}
	return new([32 * 1024]byte)[:]
}
func (b *relayBuffers) Put(v []byte) { b.Pool.Put((*[32 * 1024]byte)(v)) }

// Server handles supported AI requests and authenticated admin routes.
type Server struct {
	ReviewCache            ReviewCache
	Security               SecurityStore
	MaxBodyBytes           int64
	reviewMu               sync.Mutex
	reviewWG               sync.WaitGroup
	reviewActive           int
	reviewBusyLoggedAt     atomic.Int64
	AsyncReviewConcurrency int
	reviewContext          context.Context
	reviewCancel           context.CancelFunc
	closed                 bool
	proxy                  atomic.Pointer[proxySnapshot]
	transport              *http.Transport
	buffers                relayBuffers
	Store                  Store
	Classifier             Classifier
	AdminPassword          string
	Timeout                time.Duration
	IngressAddress         string
	TrustedProxies         []netip.Prefix
	PublicIngressURL       string
	statusMu               sync.Mutex
	classifierState        string
	lastCheckedAt          time.Time
	lastErrorKind          string
	lastErrorAt            time.Time
}

// New wires the saved configuration and classifier into an HTTP server.
func New(store Store, classifier Classifier, adminPassword string) *Server {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 1024
	transport.MaxIdleConnsPerHost = 1024
	ctx, cancel := context.WithCancel(context.Background())
	security, _ := store.(SecurityStore)
	return &Server{transport: transport, Store: store, Security: security, MaxBodyBytes: 256 << 20, AsyncReviewConcurrency: 256, reviewContext: ctx, reviewCancel: cancel, Classifier: classifier, AdminPassword: adminPassword, Timeout: 5 * time.Second}
}

// Close releases idle outbound connections owned by this gateway.
func (s *Server) Close() {
	s.reviewMu.Lock()
	s.closed = true
	s.reviewMu.Unlock()
	done := make(chan struct{})
	go func() { s.reviewWG.Wait(); close(done) }()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		s.reviewCancel()
		<-done
	}
	s.reviewCancel()
	s.transport.CloseIdleConnections()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.limitBody(w, r) {
		return
	}
	if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		_, _ = w.Write([]byte("ok"))
		return
	}
	if r.Method == http.MethodPost && protocol.Monitored(protocol.Name(r.URL.Path)) {
		s.proxyRequest(w, r)
		return
	}
	s.forward(w, r)
}

// AdminHandler exposes authenticated management routes on the management listener only.
func (s *Server) AdminHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limitBody(w, r) {
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/admin/") {
			http.NotFound(w, r)
			return
		}
		s.admin(w, r)
	})
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.Policy(r.Context())
	if err != nil {
		http.Error(w, "policy unavailable", http.StatusServiceUnavailable)
		return
	}
	r, state, err := s.prepareForward(r, p.TrustedKeyIdle())
	if err != nil {
		http.Error(w, "credential state unavailable", http.StatusServiceUnavailable)
		return
	}
	config := state.config
	cached := s.proxy.Load()
	if cached != nil && cached.baseURL == config.BaseURL {
		cached.handler.ServeHTTP(w, r)
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
			pr.Out.Header.Del("X-Real-IP")
			if ip := s.clientIP(pr.In); ip != "" {
				pr.Out.Header.Set("X-Forwarded-For", ip)
				pr.Out.Header.Set("X-Real-IP", ip)
			} else {
				pr.Out.Header.Del("X-Forwarded-For")
			}
			if peer, err := peerIP(pr.In); err == nil && s.trustsProxy(peer) {
				if proto := strings.ToLower(pr.In.Header.Get("X-Forwarded-Proto")); proto == "http" || proto == "https" {
					pr.Out.Header.Set("X-Forwarded-Proto", proto)
				}
				if host := pr.In.Header.Get("X-Forwarded-Host"); host != "" && !strings.ContainsAny(host, "/, \t\r\n?#@\\") {
					pr.Out.Header.Set("X-Forwarded-Host", host)
				}
			}
		},
		Transport: s.transport, BufferPool: &s.buffers, FlushInterval: -1, ModifyResponse: s.observeCredential,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			var large *http.MaxBytesError
			if errors.As(err, &large) {
				writeBodyError(w, err)
				return
			}
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
	s.proxy.Store(&proxySnapshot{config.BaseURL, proxy})
	proxy.ServeHTTP(w, r)
}

func (s *Server) proxyRequest(w http.ResponseWriter, r *http.Request) {
	requestID := newID()
	w.Header().Set("X-Request-Id", requestID)
	count := Count{ID: requestID, Time: time.Now().UTC(), Protocol: protocol.Name(r.URL.Path), Outcome: "request_error"}
	bypass := func(outcome string) {
		count.Outcome = outcome
		s.observeIngress(r.Context(), count)
		s.recordCount(count)
		s.forward(w, r)
	}
	p, err := s.Store.Policy(r.Context())
	if err != nil {
		count.Outcome = "gateway_error"
		s.observeIngress(r.Context(), count)
		s.recordCount(count)
		http.Error(w, "policy unavailable", http.StatusServiceUnavailable)
		return
	}
	if !p.Enabled {
		bypass("disabled")
		return
	}
	if !slices.ContainsFunc(p.Scenes, func(scene policy.Scene) bool { return scene.AppliesTo(count.Protocol) }) {
		bypass("no_scene")
		return
	}
	var state *forwardState
	r, state, err = s.prepareForward(r, p.TrustedKeyIdle())
	if err != nil {
		count.Outcome = "gateway_error"
		s.observeIngress(r.Context(), count)
		s.recordCount(count)
		http.Error(w, "credential state unavailable", http.StatusServiceUnavailable)
		return
	}
	if !state.trusted {
		bypass("untrusted_key")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.observeIngress(r.Context(), count)
		s.recordCount(count)
		writeBodyError(w, err)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	model, err := protocol.ExtractModel(r.URL.Path, r.Header.Get("Content-Type"), body)
	if err != nil {
		count.Outcome = "invalid_json"
		s.observeIngress(r.Context(), count)
		s.recordCount(count)
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return
	}
	count.Model = model
	applies := func(scene policy.Scene) bool { return scene.AppliesTo(count.Protocol) && scene.AppliesToModel(model) }
	if !slices.ContainsFunc(p.Scenes, applies) {
		bypass("no_scene")
		return
	}
	meta, err := protocol.ExtractWithContentType(r.URL.Path, r.Header.Get("Content-Type"), body)
	if err != nil {
		count.Outcome = "invalid_json"
		s.observeIngress(r.Context(), count)
		s.recordCount(count)
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return
	}
	count.UserInput = meta.Text != ""
	meta.SessionID = requestSession(r, meta.SessionID)
	s.observeIngress(r.Context(), count)
	event := s.baseEvent(r, requestID, meta, p)
	credential := ""
	if state != nil {
		credential = state.key
	}
	blockPlan := sessionPlanForRequest(r, credential, s.clientIP(r), body, meta.Protocol)
	if meta.SessionID != "" {
		event.SessionSource = "explicit"
		event.SessionRef = shortSessionRef(meta.SessionID)
	} else if blockPlan.transcript.exact != "" {
		event.SessionSource = "history"
		event.SessionRef = shortSessionRef(blockPlan.transcript.exact)
	}
	if slices.ContainsFunc(p.Scenes, func(scene policy.Scene) bool { return scene.Active() && scene.SessionBlockEnabled }) {
		if blockedKey, source := s.sessionBlocked(r.Context(), blockPlan); blockedKey != "" {
			event.SessionSource = source
			event.SessionRef = shortSessionRef(blockedKey)
			count.Outcome = "session_blocked"
			event.Kind = "warning"
			event.Decision = policy.Decision{Action: policy.Block}
			event.ErrorKind = "session_blocked"
			s.writeEvent(r.Context(), event)
			s.recordCount(count)
			writeBlock(w, meta.Protocol, requestID, p.FormatBlockMessage(requestID, r.URL.Path, meta.Model))
			return
		}
	}
	if meta.Text == "" {
		count.Outcome = "no_text"
		s.recordCount(count)
		s.forward(w, r)
		return
	}
	if !slices.ContainsFunc(p.Scenes, func(scene policy.Scene) bool { return applies(scene) && scene.Action == policy.Block }) {
		if !s.startReview(event, p, count, blockPlan) {
			count.Outcome = "review_busy"
			now := time.Now().Unix()
			last := s.reviewBusyLoggedAt.Load()
			if now-last >= 30 && s.reviewBusyLoggedAt.CompareAndSwap(last, now) {
				slog.Warn("nonblocking review capacity reached; review skipped", "limit", s.AsyncReviewConcurrency)
			}
			s.recordCount(count)
		}
		s.forward(w, r)
		return
	}
	result, decision := s.review(r.Context(), event, p, count)
	s.recordCount(result)
	if r.Context().Err() != nil {
		return
	}
	if decision.Action == policy.Block {
		if ttl := winningSceneFreezeTTL(p, decision.SceneID); ttl > 0 {
			s.rememberSessionBlock(r.Context(), blockPlan, ttl)
		}
		writeBlock(w, meta.Protocol, requestID, p.FormatBlockMessage(requestID, r.URL.Path, meta.Model))
		return
	}
	s.forward(w, r)
}

func shortSessionRef(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func (s *Server) baseEvent(r *http.Request, requestID string, meta protocol.Request, p policy.Policy) Event {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	event := Event{ID: newID(), Time: time.Now().UTC(), RequestID: requestID,
		Protocol: meta.Protocol, EndpointGroup: protocol.Group(meta.Protocol), ImageOperation: protocol.ImageOperation(meta.Protocol), Endpoint: r.URL.Path, Model: meta.Model, Stream: meta.Stream,
		HasNonText: meta.HasNonText, Text: meta.Text, InputChars: utf8.RuneCountInString(meta.Text),
		ContentType: contentType, RequestBytes: r.ContentLength,
		Parameters: meta.Parameters, ClientIP: s.clientIP(r), UserAgent: privacy.RedactText(r.UserAgent()),
		SessionID: meta.SessionID, ClientRequestID: requestHeader(r, "X-Client-Request-Id", "X-Request-Id")}
	if state, ok := r.Context().Value(forwardStateKey{}).(*forwardState); ok {
		event.CredentialID = state.key
	}
	if event.SessionID == "" {
		event.SessionID = meta.SessionID
	}
	if p.PreviewChars != nil {
		event.TextPreview = preview(meta.Text, *p.PreviewChars)
	}
	return event
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
	s.lastErrorKind = classifierErrorKind(err, timedOut)
}

func classifierErrorKind(err error, timedOut bool) string {
	if timedOut {
		return "classifier_timeout"
	}
	if errors.Is(err, ErrInvalidClassifierResponse) {
		return "classifier_invalid_response"
	}
	return "classifier_unavailable"
}

func (s *Server) writeEvent(ctx context.Context, e Event) {
	e.Redact()
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
	text = privacy.RedactText(text)
	r := []rune(strings.TrimSpace(text))
	if len(r) > length {
		r = r[:length]
	}
	return string(r)
}

func requestHeader(r *http.Request, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			return privacy.RedactText(value)
		}
	}
	return ""
}

func writeBlock(w http.ResponseWriter, protocolName, requestID, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-Id", requestID)
	w.Header().Set("X-Sael-Blocked", "prompt_guard")
	if protocolName == "anthropic" {
		w.Header().Set("Request-Id", requestID)
	}
	w.WriteHeader(http.StatusForbidden)
	var result any
	switch protocolName {
	case "anthropic":
		result = map[string]any{"type": "error", "error": map[string]any{"type": "permission_error", "code": "prompt_guard_blocked", "message": message}}
	case "openai_responses":
		result = map[string]any{"error": map[string]any{"type": "api_error", "code": "prompt_guard_blocked", "message": message}}
	default:
		result = map[string]any{"error": map[string]any{"type": "permission_error", "code": "prompt_guard_blocked", "message": message}}
	}
	_ = json.NewEncoder(w).Encode(result)
}
