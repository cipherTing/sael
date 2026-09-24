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

// Scene combines question hits into one ordered action rule.
type Scene struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Questions []string `json:"questions"`
	Match     Match    `json:"match"`
	Action    Action   `json:"action"`
}

// Policy is one versioned snapshot used for a whole request.
type Policy struct {
	Enabled         bool               `json:"enabled"`
	Version         int64              `json:"version"`
	Thresholds      map[string]float64 `json:"thresholds"`
	Scenes          []Scene            `json:"scenes"`
	UnmatchedAction Action             `json:"unmatched_action"`
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

// Validate checks ranges, references, and required fields before a policy is enabled.
func Validate(p Policy) error {
	if p.UnmatchedAction != "" && p.UnmatchedAction != Allow && p.UnmatchedAction != Block {
		return errors.New("invalid unmatched action")
	}
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
	for key, value := range p.Thresholds {
		q, ok := known[key]
		if !ok {
			return fmt.Errorf("unknown question %q", key)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > q.Max {
			return fmt.Errorf("threshold for %s must be 0–%g", key, q.Max)
		}
	}
	if p.Enabled {
		for _, q := range Questions {
			if _, ok := p.Thresholds[q.Key]; !ok {
				return fmt.Errorf("threshold for %s is required", q.Key)
			}
		}
		if p.UnmatchedAction == "" {
			return errors.New("unmatched action is required")
		}
		if p.PreviewChars == nil || p.RetentionDays == nil {
			return errors.New("preview chars and retention days are required")
		}
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
		if len(scene.Questions) == 0 {
			return fmt.Errorf("scene %s needs a question", scene.Name)
		}
		chosen := map[string]bool{}
		for _, key := range scene.Questions {
			if _, ok := known[key]; !ok || chosen[key] {
				return fmt.Errorf("invalid question %q in scene %s", key, scene.Name)
			}
			chosen[key] = true
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
	hitSet := map[string]bool{}
	for _, q := range Questions {
		answer := byKey[q.Key]
		threshold := p.Thresholds[q.Key]
		if answer.Value > threshold {
			decision.Hits = append(decision.Hits, Hit{q.Key, answer.Value, threshold})
			hitSet[q.Key] = true
		}
	}
	if len(decision.Hits) == 0 {
		return decision, nil
	}
	decision.Action = p.UnmatchedAction
	for i, scene := range p.Scenes {
		matches := 0
		for _, key := range scene.Questions {
			if hitSet[key] {
				matches++
			}
		}
		if (scene.Match == Any && matches == 0) || (scene.Match == All && matches != len(scene.Questions)) {
			continue
		}
		if decision.SceneID == "" {
			decision.SceneID, decision.SceneName, decision.ScenePriority, decision.Action = scene.ID, scene.Name, i+1, scene.Action
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
