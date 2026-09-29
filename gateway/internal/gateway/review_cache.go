package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

// ReviewCache contains scene predicates only; request identity and actions remain live.
type ReviewCache interface {
	Lookup(context.Context, []string) (map[string]bool, error)
	Save(context.Context, map[string]bool) error
}

// ReviewCacheConfig controls verdict retention and disposable Redis capacity.
type ReviewCacheConfig struct {
	TTLDays  int   `json:"ttl_days"`
	MaxBytes int64 `json:"max_bytes"`
}

// Validate checks cache retention and capacity bounds.
func (c ReviewCacheConfig) Validate() error {
	if c.TTLDays < 1 || c.TTLDays > 3650 || c.MaxBytes < 1<<20 || c.MaxBytes > 1<<40 {
		return errors.New("有效期须为 1–3650 天，容量须为 1 MiB–1 TiB")
	}
	return nil
}

// ReviewCacheStatus combines saved settings, live memory usage and reuse counts.
type ReviewCacheStatus struct {
	ReviewCacheConfig
	Available         bool  `json:"available"`
	UsedBytes         int64 `json:"used_bytes"`
	EffectiveMaxBytes int64 `json:"effective_max_bytes"`
	Entries           int64 `json:"entries"`
	Lookups           int64 `json:"lookups"`
	Hits              int64 `json:"hits"`
}

// ReviewCacheAdmin exposes cache settings and operational statistics.
type ReviewCacheAdmin interface {
	Status(context.Context) (ReviewCacheStatus, error)
	Configure(context.Context, ReviewCacheConfig) error
}

func sceneCacheKey(c JevConfig, scene policy.Scene, text string) string {
	conditions := slices.Clone(scene.Conditions)
	slices.SortFunc(conditions, func(a, b policy.Condition) int {
		if a.Question < b.Question {
			return -1
		}
		if a.Question > b.Question {
			return 1
		}
		return 0
	})
	digest := func(v any) string {
		raw, _ := json.Marshal(v)
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	prompt := sha256.Sum256([]byte(text))
	return "sael:review:v1:" + digest(struct {
		Config    JevConfig
		Questions []policy.Question
	}{c, policy.Questions}) + ":" + digest(struct {
		Match      policy.Match
		Conditions []policy.Condition
	}{scene.Match, conditions}) + ":" + hex.EncodeToString(prompt[:])
}

func cachedDecision(p policy.Policy, values map[string]bool, endpoint, model string) (policy.Decision, []policy.SceneTrace) {
	d := policy.Decision{Action: policy.Allow, Hits: []policy.Hit{}, AlsoMatched: []string{}}
	trace := make([]policy.SceneTrace, 0, len(p.Scenes))
	for i, s := range p.Scenes {
		t := policy.SceneTrace{ID: s.ID, Name: s.Name, Status: "not_matched"}
		switch {
		case !s.Active():
			t.Status = "disabled"
		case !s.AppliesTo(endpoint):
			t.Status = "endpoint_skipped"
		case !s.AppliesToModel(model):
			t.Status = "model_skipped"
		case values[s.ID]:
			if d.SceneID == "" {
				d.SceneID = s.ID
				d.SceneName = s.Name
				d.ScenePriority = i + 1
				d.Action = s.Action
				t.Status = "effective"
			} else {
				d.AlsoMatched = append(d.AlsoMatched, s.ID)
				t.Status = "shadowed"
			}
		}
		trace = append(trace, t)
	}
	return d, trace
}
