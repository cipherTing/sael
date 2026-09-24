// Package policy validates thresholds and applies ordered scene rules to classifier answers.
package policy

import (
	"errors"
	"fmt"
	"math"
)

// Question describes an expected classifier key, type, and score range.
type Question struct {
	Key  string  `json:"key"`
	Type string  `json:"type"`
	Max  float64 `json:"max"`
}

// Questions is the gateway catalog of eleven CLI answer keys.
var Questions = []Question{
	{"cyber_abuse", "noul", 1}, {"illicit", "noul", 1},
	{"violence", "noul", 1}, {"child_safety", "noul", 1},
	{"hate_harassment", "noul", 1}, {"privacy_pii", "noul", 1},
	{"fraud_deception", "noul", 1}, {"self_harm", "noul", 1},
	{"bypass_attempt", "noul", 1}, {"sexual", "score", 3},
	{"gore", "score", 3},
}

// Action determines whether a hit is forwarded or blocked.
type Action string

const (
	// Allow records a hit and forwards the original request.
	Allow Action = "allow"
	// Block records a hit and rejects the request.
	Block Action = "block"
)

// Match controls how a scene combines its selected questions.
type Match string

const (
	// Any matches when at least one selected question hits.
	Any Match = "any"
	// All matches when every selected question hits.
	All Match = "all"
)

// Condition compares one classifier score with a threshold belonging to a scene.
type Condition struct {
	Question  string  `json:"question"`
	Threshold float64 `json:"threshold"`
}

// Scene combines score conditions into one ordered action rule.
type Scene struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Note       string      `json:"note,omitempty"`
	Conditions []Condition `json:"conditions"`
	Questions  []string    `json:"questions,omitempty"` // Legacy policies are converted on load.
	Match      Match       `json:"match"`
	Action     Action      `json:"action"`
}

// Policy is one versioned snapshot used for a whole request.
type Policy struct {
	Enabled         bool               `json:"enabled"`
	Version         int64              `json:"version"`
	Thresholds      map[string]float64 `json:"thresholds,omitempty"` // Legacy policies are converted on load.
	Scenes          []Scene            `json:"scenes"`
	UnmatchedAction Action             `json:"unmatched_action,omitempty"` // Legacy policies are converted on load.
	PreviewChars    *int               `json:"preview_chars"`
	RetentionDays   *int               `json:"retention_days"`
}

// Answer is one measurement returned by the Sael CLI.
type Answer struct {
	Question string  `json:"question"`
	Type     string  `json:"type"`
	Value    float64 `json:"value"`
}

// Hit preserves a score and the threshold it exceeded.
type Hit struct {
	Question  string  `json:"question"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
}

// Decision holds the first matching scene and final action.
type Decision struct {
	Action        Action   `json:"action"`
	Hits          []Hit    `json:"hits"`
	SceneID       string   `json:"scene_id,omitempty"`
	SceneName     string   `json:"scene_name,omitempty"`
	ScenePriority int      `json:"scene_priority,omitempty"`
	AlsoMatched   []string `json:"also_matched,omitempty"`
}

// UpgradeLegacy moves the old shared thresholds into each scene once.
func UpgradeLegacy(p *Policy) {
	if p.Enabled && len(p.Scenes) == 0 {
		p.Enabled = false
	}
	for i := range p.Scenes {
		scene := &p.Scenes[i]
		if len(scene.Conditions) == 0 && len(scene.Questions) > 0 {
			for _, key := range scene.Questions {
				if threshold, ok := p.Thresholds[key]; ok {
					scene.Conditions = append(scene.Conditions, Condition{Question: key, Threshold: threshold})
				}
			}
		}
		scene.Questions = nil
	}
	p.Thresholds = nil
	p.UnmatchedAction = ""
}

// Validate checks ranges, references, and required fields before a policy is enabled.
func Validate(p Policy) error {
	if p.PreviewChars != nil && (*p.PreviewChars < 0 || *p.PreviewChars > 10000) {
		return errors.New("preview chars must be 0–10000")
	}
	if p.RetentionDays != nil && (*p.RetentionDays < 1 || *p.RetentionDays > 3650) {
		return errors.New("retention days must be 1–3650")
	}
	known := map[string]Question{}
	for _, q := range Questions {
		known[q.Key] = q
	}
	if p.Enabled && len(p.Scenes) == 0 {
		return errors.New("at least one scene is required to enable review")
	}
	seen := map[string]bool{}
	for _, scene := range p.Scenes {
		if scene.ID == "" || scene.Name == "" || seen[scene.ID] {
			return errors.New("scene requires a unique id and name")
		}
		seen[scene.ID] = true
		if scene.Match != Any && scene.Match != All {
			return fmt.Errorf("invalid match for %s", scene.Name)
		}
		if scene.Action != Allow && scene.Action != Block {
			return fmt.Errorf("invalid action for %s", scene.Name)
		}
		if len(scene.Conditions) == 0 {
			return fmt.Errorf("scene %s needs a condition", scene.Name)
		}
		chosen := map[string]bool{}
		for _, condition := range scene.Conditions {
			q, ok := known[condition.Question]
			if !ok || chosen[condition.Question] {
				return fmt.Errorf("invalid question %q in scene %s", condition.Question, scene.Name)
			}
			if math.IsNaN(condition.Threshold) || math.IsInf(condition.Threshold, 0) || condition.Threshold < 0 || condition.Threshold > q.Max {
				return fmt.Errorf("threshold for %s must be 0–%g", condition.Question, q.Max)
			}
			chosen[condition.Question] = true
		}
	}
	return nil
}

// Evaluate validates all CLI answers and applies ordered scene rules.
func Evaluate(p Policy, answers []Answer) (Decision, error) {
	if err := Validate(p); err != nil {
		return Decision{}, err
	}
	if !p.Enabled {
		return Decision{Action: Allow}, nil
	}
	byKey, err := checkedAnswers(answers)
	if err != nil {
		return Decision{}, err
	}
	decision := Decision{Action: Allow, Hits: []Hit{}}
	for i, scene := range p.Scenes {
		hits := make([]Hit, 0, len(scene.Conditions))
		for _, condition := range scene.Conditions {
			if value := byKey[condition.Question].Value; value > condition.Threshold {
				hits = append(hits, Hit{condition.Question, value, condition.Threshold})
			}
		}
		if (scene.Match == Any && len(hits) == 0) || (scene.Match == All && len(hits) != len(scene.Conditions)) {
			continue
		}
		if decision.SceneID == "" {
			decision.SceneID, decision.SceneName, decision.ScenePriority, decision.Action, decision.Hits = scene.ID, scene.Name, i+1, scene.Action, hits
		} else {
			decision.AlsoMatched = append(decision.AlsoMatched, scene.ID)
		}
	}
	return decision, nil
}

// ValidateAnswers checks that one classifier response has all eleven valid scores.
func ValidateAnswers(answers []Answer) error {
	_, err := checkedAnswers(answers)
	return err
}

func checkedAnswers(answers []Answer) (map[string]Answer, error) {
	byKey := make(map[string]Answer, len(answers))
	known := map[string]Question{}
	for _, q := range Questions {
		known[q.Key] = q
	}
	for _, answer := range answers {
		q, ok := known[answer.Question]
		if !ok || q.Type != answer.Type || math.IsNaN(answer.Value) || math.IsInf(answer.Value, 0) || answer.Value < 0 || answer.Value > q.Max {
			return nil, fmt.Errorf("invalid answer for %s", answer.Question)
		}
		if _, duplicate := byKey[answer.Question]; duplicate {
			return nil, fmt.Errorf("duplicate answer for %s", answer.Question)
		}
		byKey[answer.Question] = answer
	}
	if len(byKey) != len(Questions) {
		return nil, errors.New("incomplete classifier answers")
	}
	return byKey, nil
}
