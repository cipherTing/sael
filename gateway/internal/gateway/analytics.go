package gateway

import (
	"time"
	_ "time/tzdata" // The minimal Docker image has no system time zone database.
)

// SceneMatch identifies a matching scene and the scene that won priority.
type SceneMatch struct {
	SceneID    string `json:"scene_id"`
	Name       string `json:"name"`
	Action     string `json:"action"`
	WinnerID   string `json:"winner_id"`
	WinnerName string `json:"winner_name"`
}

// AnalyticsFilter applies the same time and request dimensions to all analytics.
type AnalyticsFilter struct {
	Since, Until          time.Time
	Endpoint, Model       string
	Granularity, Timezone string
}

func (f AnalyticsFilter) StepSeconds() int64 {
	if seconds := map[string]int64{"1m": 60, "5m": 300, "1h": 3600, "1d": 86400}[f.Granularity]; seconds != 0 {
		return seconds
	}
	return 3600
}

// TrafficPoint contains aggregate request outcomes for a time bucket.
type TrafficPoint struct {
	Time     time.Time `json:"time"`
	Endpoint string    `json:"endpoint"`
	Model    string    `json:"model"`
	Outcome  string    `json:"outcome"`
	Count    int64     `json:"count"`
}

// DistributionPoint contains one non-cumulative histogram bucket.
type DistributionPoint struct {
	Time     time.Time `json:"time"`
	Endpoint string    `json:"endpoint"`
	Model    string    `json:"model"`
	Metric   string    `json:"metric"`
	Upper    float64   `json:"upper"`
	Count    int64     `json:"count"`
}

// ScenePoint counts a scene match at its request time.
type ScenePoint struct {
	Time     time.Time `json:"time"`
	Endpoint string    `json:"endpoint"`
	SceneMatch
	Count int64 `json:"count"`
}

// ErrorPoint counts a Jev failure category.
type ErrorPoint struct {
	Time     time.Time `json:"time"`
	Endpoint string    `json:"endpoint"`
	Kind     string    `json:"kind"`
	Count    int64     `json:"count"`
}

// Analytics combines traffic and distributions without requiring normal request logs.
type Analytics struct {
	CurrentRPM    *int64              `json:"current_rpm,omitempty"`
	StepSeconds   int64               `json:"step_seconds"`
	Since         time.Time           `json:"since"`
	Until         time.Time           `json:"until"`
	Traffic       []TrafficPoint      `json:"traffic"`
	Previous      []TrafficPoint      `json:"previous"`
	Distributions []DistributionPoint `json:"distributions"`
	Scenes        []ScenePoint        `json:"scenes"`
	Errors        []ErrorPoint        `json:"errors"`
	Models        []string            `json:"models"`
}
