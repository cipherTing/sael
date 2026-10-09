package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

// ErrInvalidReviewAPIKey identifies missing, unknown, or revoked review credentials.
var ErrInvalidReviewAPIKey = errors.New("invalid review API key")

// ReviewAPIKey is the administrative view of a dedicated HTTP review key.
// Secret is only populated by test implementations and the one-time creation response.
type ReviewAPIKey struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Note         string     `json:"note,omitempty"`
	Prefix       string     `json:"prefix"`
	Masked       string     `json:"masked"`
	Secret       string     `json:"secret,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RequestCount int64      `json:"request_count,omitempty"`
}

// ReviewAPIStat is independent from gateway ingress counters.
type ReviewAPIStat struct {
	Time          time.Time        `json:"time"`
	RequestSource string           `json:"request_source"`
	APIKeyID      string           `json:"api_key_id"`
	Outcome       string           `json:"outcome"`
	DurationMS    int64            `json:"duration_ms"`
	CacheHit      bool             `json:"cache_hit"`
	SceneID       string           `json:"scene_id,omitempty"`
	SceneName     string           `json:"scene_name,omitempty"`
	Scenes        []ReviewAPIScene `json:"scenes,omitempty"`
}

// ReviewAPIOverview aggregates the independent HTTP review request counters.
type ReviewAPIOverview struct {
	Since     time.Time        `json:"since"`
	Until     time.Time        `json:"until"`
	Requests  int64            `json:"requests"`
	RPM       int64            `json:"rpm"`
	Allowed   int64            `json:"allowed"`
	Hits      int64            `json:"hits"`
	Blocked   int64            `json:"blocked"`
	Errors    int64            `json:"errors"`
	CacheHits int64            `json:"cache_hits"`
	P50MS     int64            `json:"p50_ms"`
	P95MS     int64            `json:"p95_ms"`
	Outcomes  []NamedCount     `json:"outcomes"`
	Scenes    []NamedCount     `json:"scenes"`
	Keys      []NamedCount     `json:"keys"`
	Trend     []ReviewAPITrend `json:"trend"`
}

// ReviewAPITrend is one minute of HTTP review activity and latency.
type ReviewAPITrend struct {
	Time     time.Time `json:"time"`
	Requests int64     `json:"requests"`
	Hits     int64     `json:"hits"`
	Blocked  int64     `json:"blocked"`
	Errors   int64     `json:"errors"`
	Duration int64     `json:"duration_ms"`
	P50MS    int64     `json:"p50_ms"`
	P95MS    int64     `json:"p95_ms"`
}

// ReviewAPIStore provides dedicated review credentials and statistics.
type ReviewAPIStore interface {
	AuthenticateReviewAPIKey(context.Context, string) (ReviewAPIKey, error)
	CreateReviewAPIKey(context.Context, string, string) (ReviewAPIKey, error)
	ListReviewAPIKeys(context.Context) ([]ReviewAPIKey, error)
	RevokeReviewAPIKey(context.Context, string) error
	RecordReviewAPIStat(context.Context, ReviewAPIStat) error
	ReviewAPIOverview(context.Context, time.Time, time.Time) (ReviewAPIOverview, error)
}

type reviewAPIResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Flagged bool             `json:"flagged"`
	Action  policy.Action    `json:"action"`
	Scenes  []ReviewAPIScene `json:"scenes"`
	Scores  []ReviewAPIScore `json:"scores"`
}

// ReviewAPIScore is a real classifier measurement; cache hits have no scores.
type ReviewAPIScore struct {
	Question string  `json:"question"`
	Value    float64 `json:"value"`
}

// ReviewAPIScene describes the sole winning scene and its matched conditions.
type ReviewAPIScene struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Matched bool         `json:"matched"`
	Hits    []policy.Hit `json:"hits"`
}
