// Package policy validates thresholds and applies ordered scene rules to classifier answers.
package policy

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cipherTing/sael/gateway/internal/protocol"
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

// ReviewMode determines when a gateway request waits for classification.
type ReviewMode string

const (
	// Blocking waits for classification before forwarding.
	Blocking ReviewMode = "blocking"
	// NonBlocking classifies after forwarding and can only record hits.
	NonBlocking ReviewMode = "non_blocking"
)

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
	Question   string  `json:"question"`
	Threshold  float64 `json:"threshold"`
	RecordOnly bool    `json:"record_only,omitempty"`
}

// ConditionKey identifies a comparison independently of its disposition.
type ConditionKey struct {
	Question  string
	Threshold float64
}

// Key excludes disposition from the identity of a threshold comparison.
func (c Condition) Key() ConditionKey { return ConditionKey{c.Question, c.Threshold} }

// Scene combines score conditions into one ordered action rule.
type Scene struct {
	NeedsEndpointSelection bool        `json:"needs_endpoint_selection,omitempty"`
	ID                     string      `json:"id"`
	Name                   string      `json:"name"`
	Note                   string      `json:"note,omitempty"`
	Conditions             []Condition `json:"conditions"`
	Questions              []string    `json:"questions,omitempty"` // Legacy policies are converted on load.
	Match                  Match       `json:"match"`
	Action                 Action      `json:"action"`
	ReviewMode             ReviewMode  `json:"review_mode,omitempty"`
	Enabled                *bool       `json:"enabled,omitempty"`
	Endpoints              []string    `json:"endpoints,omitempty"`
	Models                 []string    `json:"models,omitempty"`
	SessionBlockEnabled    bool        `json:"session_block_enabled,omitempty"`
	SessionBlockTTLSeconds int         `json:"session_block_ttl_seconds,omitempty"`
}

// Mode also supports policies that have not yet been converted on load.
func (s Scene) Mode() ReviewMode {
	if s.ReviewMode != "" {
		return s.ReviewMode
	}
	if s.Action == Allow {
		return NonBlocking
	}
	return Blocking
}

func (s Scene) recordsOnly(c Condition) bool {
	if s.ReviewMode == "" {
		return s.Action == Allow
	}
	if s.Match == All {
		return s.Action == Allow
	}
	return c.RecordOnly
}

// CanReject controls configuration of freezing, not whether this input hits.
func (s Scene) CanReject() bool {
	if s.Mode() == NonBlocking {
		return false
	}
	for _, c := range s.Conditions {
		if !s.recordsOnly(c) {
			return true
		}
	}
	return false
}

// Active preserves the enabled state of scenes created before per-scene switches existed.
func (s Scene) Active() bool { return !s.NeedsEndpointSelection && (s.Enabled == nil || *s.Enabled) }

// AppliesTo reports whether an enabled scene includes the request endpoint.
func (s Scene) AppliesTo(endpoint string) bool {
	return s.Active() && (len(s.Endpoints) == 0 || slices.Contains(s.Endpoints, protocol.Group(endpoint)))
}

// AppliesToModel reports whether a scene includes the request model.
func (s Scene) AppliesToModel(model string) bool {
	return len(s.Models) == 0 || slices.Contains(s.Models, model)
}

// RequiredQuestions returns the distinct questions for scenes that can affect this request.
func RequiredQuestions(p Policy, endpoint, model string) []string {
	if !p.Enabled {
		return nil
	}
	needed := make(map[string]bool)
	for _, scene := range p.Scenes {
		if !scene.Active() || (endpoint != "" && !scene.AppliesTo(endpoint)) || !scene.AppliesToModel(model) {
			continue
		}
		for _, condition := range scene.Conditions {
			needed[condition.Question] = true
		}
	}
	keys := make([]string, 0, len(needed))
	for _, question := range Questions {
		if needed[question.Key] {
			keys = append(keys, question.Key)
		}
	}
	return keys
}

// Policy is one configuration snapshot used for a whole request.
type Policy struct {
	TrustedKeyIdleDays int                `json:"trusted_key_idle_days"`
	Enabled            bool               `json:"enabled"`
	ReviewAPIEnabled   bool               `json:"review_api_enabled"`
	Thresholds         map[string]float64 `json:"thresholds,omitempty"` // Legacy policies are converted on load.
	Scenes             []Scene            `json:"scenes"`
	UnmatchedAction    Action             `json:"unmatched_action,omitempty"` // Legacy policies are converted on load.
	PreviewChars       *int               `json:"preview_chars"`
	RetentionDays      *int               `json:"retention_days"`
	BlockMessage       string             `json:"block_message,omitempty"`
}

// DefaultBlockMessage is the client-facing text used when no template is configured.
const DefaultBlockMessage = "Request denied."

var blockMessagePlaceholders = regexp.MustCompile(`\{[^{}]*\}`)

// FormatBlockMessage renders only request context. Scene names and hit details
// are deliberately unavailable to the client-facing error template.
func (p Policy) FormatBlockMessage(requestID, endpoint, model string) string {
	message := p.BlockMessage
	if strings.TrimSpace(message) == "" {
		message = DefaultBlockMessage
	}
	return strings.NewReplacer(
		"{request_id}", requestID,
		"{endpoint}", endpoint,
		"{model}", model,
	).Replace(message)
}

// Answer is one measurement returned by the Sael CLI.
type Answer struct {
	Question string  `json:"question"`
	Type     string  `json:"type"`
	Value    float64 `json:"value"`
}

// Hit preserves a score and the threshold it exceeded.
type Hit struct {
	Question   string   `json:"question"`
	Value      *float64 `json:"value,omitempty"`
	Threshold  float64  `json:"threshold"`
	RecordOnly *bool    `json:"record_only,omitempty"`
}

// Decision holds the first matching scene and final action.
type Decision struct {
	Action        Action     `json:"action"`
	ReviewMode    ReviewMode `json:"review_mode,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	Hits          []Hit      `json:"hits"`
	SceneID       string     `json:"scene_id,omitempty"`
	SceneName     string     `json:"scene_name,omitempty"`
	ScenePriority int        `json:"scene_priority,omitempty"`
	AlsoMatched   []string   `json:"also_matched,omitempty"`
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
		if scene.ReviewMode == "" && (scene.Action == Allow || scene.Action == Block) {
			scene.ReviewMode = scene.Mode()
			for j := range scene.Conditions {
				scene.Conditions[j].RecordOnly = scene.Action == Allow
			}
			if scene.Action == Allow {
				scene.SessionBlockEnabled = false
			}
		}
		if scene.Match == Any && scene.ReviewMode != "" {
			scene.Action = Allow
			for _, c := range scene.Conditions {
				if !c.RecordOnly {
					scene.Action = Block
					break
				}
			}
		}
		if len(scene.Endpoints) > 0 {
			normalized := []string{}
			for _, endpoint := range scene.Endpoints {
				if endpoint == "openai_images_variations" {
					continue
				}
				group := protocol.Group(endpoint)
				if !slices.Contains(normalized, group) {
					normalized = append(normalized, group)
				}
			}
			if len(normalized) == 0 {
				off := false
				scene.Enabled = &off
				scene.Endpoints = []string{"openai_images"}
				scene.NeedsEndpointSelection = true
			} else {
				scene.Endpoints = normalized
			}
		}
	}
	p.Thresholds = nil
	p.UnmatchedAction = ""
}

// TrustedKeyIdle returns the configured trust lifetime, defaulting to thirty days.
func (p Policy) TrustedKeyIdle() time.Duration {
	days := p.TrustedKeyIdleDays
	if days <= 0 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

// Validate checks ranges, references, and required fields before a policy is enabled.
func Validate(p Policy) error {
	if utf8.RuneCountInString(p.BlockMessage) > 500 {
		return errors.New("拦截提示不能超过 500 字")
	}
	for _, placeholder := range blockMessagePlaceholders.FindAllString(p.BlockMessage, -1) {
		if placeholder != "{request_id}" && placeholder != "{endpoint}" && placeholder != "{model}" {
			return fmt.Errorf("不支持拦截提示占位符 %s", placeholder)
		}
	}
	if strings.ContainsAny(blockMessagePlaceholders.ReplaceAllString(p.BlockMessage, ""), "{}") {
		return errors.New("拦截提示包含不完整的占位符")
	}
	if p.TrustedKeyIdleDays < 0 || p.TrustedKeyIdleDays > 106751 {
		return errors.New("可信密钥闲置天数必须为正整数且不超过 106751")
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
	if p.Enabled && len(p.Scenes) == 0 {
		return errors.New("at least one scene is required to enable review")
	}
	seen := map[string]bool{}
	for _, scene := range p.Scenes {
		if scene.ReviewMode != "" && scene.ReviewMode != Blocking && scene.ReviewMode != NonBlocking {
			return fmt.Errorf("场景 %s 的审查方式无效", scene.Name)
		}
		if scene.ReviewMode == NonBlocking {
			for _, c := range scene.Conditions {
				if !scene.recordsOnly(c) {
					return fmt.Errorf("非阻塞场景 %s 只能仅记录", scene.Name)
				}
			}
		}
		if scene.ReviewMode != "" && scene.SessionBlockEnabled && !scene.CanReject() {
			return fmt.Errorf("仅记录场景 %s 不能冻结会话", scene.Name)
		}
		if scene.SessionBlockEnabled && (scene.SessionBlockTTLSeconds < 1 || scene.SessionBlockTTLSeconds > 9223372036) {
			return fmt.Errorf("场景 %s 的会话冻结时长无效", scene.Name)
		}
		if scene.NeedsEndpointSelection && (scene.Enabled == nil || *scene.Enabled) {
			return fmt.Errorf("场景 %s 需要重新选择端点", scene.Name)
		}
		models := map[string]bool{}
		for _, model := range scene.Models {
			if model == "" || strings.TrimSpace(model) != model || models[model] {
				return fmt.Errorf("场景 %s 的模型为空、重复或含首尾空格", scene.Name)
			}
			models[model] = true
		}
		endpoints := map[string]bool{}
		for _, endpoint := range scene.Endpoints {
			if !protocol.Monitored(endpoint) || endpoints[endpoint] {
				return fmt.Errorf("场景 %s 的端点无效或重复", scene.Name)
			}
			endpoints[endpoint] = true
		}
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

// ConditionTrace preserves the actual comparison used during a simulation.
type ConditionTrace struct {
	Question   string   `json:"question"`
	Value      *float64 `json:"value,omitempty"`
	Threshold  float64  `json:"threshold"`
	Matched    bool     `json:"matched"`
	RecordOnly *bool    `json:"record_only,omitempty"`
}

// SceneTrace explains why a scene was selected or skipped.
type SceneTrace struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Status     string           `json:"status"`
	Conditions []ConditionTrace `json:"conditions"`
}

// Evaluate uses the same evaluator as the operator's draft simulation.
func Evaluate(p Policy, answers []Answer, endpoint ...string) (Decision, error) {
	scope := ""
	if len(endpoint) > 0 {
		scope = endpoint[0]
	}
	model := ""
	if len(endpoint) > 1 {
		model = endpoint[1]
	}
	d, _, err := EvaluateDetailed(p, answers, scope, model)
	return d, err
}

// EvaluateDetailed produces the production decision and its comparison trace in one pass.
func EvaluateDetailed(p Policy, answers []Answer, endpoint string, model ...string) (Decision, []SceneTrace, error) {
	requestModel := ""
	if len(model) > 0 {
		requestModel = model[0]
	}
	byKey, err := checkedAnswersFor(answers, RequiredQuestions(p, endpoint, requestModel), false)
	if err != nil {
		return Decision{}, nil, err
	}
	return evaluateConditions(p, endpoint, requestModel, func(c Condition) (*float64, bool, error) {
		value := byKey[c.Question].Value
		return &value, value > c.Threshold, nil
	})
}

// EvaluatePredicates reuses condition facts without fabricating classifier scores.
func EvaluatePredicates(p Policy, facts map[ConditionKey]bool, endpoint, model string) (Decision, []SceneTrace, error) {
	return evaluateConditions(p, endpoint, model, func(c Condition) (*float64, bool, error) {
		matched, ok := facts[c.Key()]
		if !ok {
			return nil, false, fmt.Errorf("missing cached condition %s", c.Question)
		}
		return nil, matched, nil
	})
}

func evaluateConditions(p Policy, endpoint, requestModel string, compare func(Condition) (*float64, bool, error)) (Decision, []SceneTrace, error) {
	if err := Validate(p); err != nil {
		return Decision{}, nil, err
	}
	decision := Decision{Action: Allow, Hits: []Hit{}, AlsoMatched: []string{}}
	traces := []SceneTrace{}
	if !p.Enabled {
		return decision, traces, nil
	}
	for i, scene := range p.Scenes {
		trace := SceneTrace{ID: scene.ID, Name: scene.Name, Status: "not_matched", Conditions: []ConditionTrace{}}
		if decision.SceneID != "" {
			trace.Status = "priority_skipped"
			traces = append(traces, trace)
			continue
		}
		if !scene.Active() {
			trace.Status = "disabled"
			traces = append(traces, trace)
			continue
		}
		if endpoint != "" && !scene.AppliesTo(endpoint) {
			trace.Status = "endpoint_skipped"
			traces = append(traces, trace)
			continue
		}
		if !scene.AppliesToModel(requestModel) {
			trace.Status = "model_skipped"
			traces = append(traces, trace)
			continue
		}
		hits := []Hit{}
		action := Allow
		for _, condition := range scene.Conditions {
			value, matched, err := compare(condition)
			if err != nil {
				return Decision{}, nil, err
			}
			recordOnly := scene.recordsOnly(condition)
			trace.Conditions = append(trace.Conditions, ConditionTrace{condition.Question, value, condition.Threshold, matched, &recordOnly})
			if matched {
				hits = append(hits, Hit{condition.Question, value, condition.Threshold, &recordOnly})
				if !recordOnly {
					action = Block
				}
			}
		}
		matched := (scene.Match == Any && len(hits) > 0) || (scene.Match == All && len(hits) == len(scene.Conditions))
		if matched {
			decision.SceneID, decision.SceneName, decision.ScenePriority, decision.Action, decision.Hits = scene.ID, scene.Name, i+1, action, hits
			decision.ReviewMode = scene.Mode()
			decision.Reason = "reject"
			if action == Allow {
				switch {
				case scene.Mode() == NonBlocking:
					decision.Reason = "non_blocking"
				case scene.Match == All:
					decision.Reason = "scene_record_only"
				default:
					decision.Reason = "condition_record_only"
				}
			}
			trace.Status = "effective"
		}
		traces = append(traces, trace)
	}
	return decision, traces, nil
}

// ValidateAnswers checks that one classifier response has all eleven valid scores.
func ValidateAnswers(answers []Answer) error {
	keys := make([]string, 0, len(Questions))
	for _, q := range Questions {
		keys = append(keys, q.Key)
	}
	_, err := checkedAnswersFor(answers, keys, true)
	return err
}

// ValidateAnswersFor requires exactly the scores requested from the classifier.
func ValidateAnswersFor(answers []Answer, required []string) error {
	if len(required) == 0 {
		return errors.New("no classifier questions requested")
	}
	_, err := checkedAnswersFor(answers, required, true)
	return err
}

func checkedAnswersFor(answers []Answer, required []string, exact bool) (map[string]Answer, error) {
	byKey := make(map[string]Answer, len(answers))
	known := map[string]Question{}
	for _, q := range Questions {
		known[q.Key] = q
	}
	wanted := make(map[string]bool, len(required))
	for _, key := range required {
		if _, ok := known[key]; !ok || wanted[key] {
			return nil, fmt.Errorf("invalid requested question %q", key)
		}
		wanted[key] = true
	}
	for _, answer := range answers {
		q, ok := known[answer.Question]
		if !ok || q.Type != answer.Type || math.IsNaN(answer.Value) || math.IsInf(answer.Value, 0) || answer.Value < 0 || answer.Value > q.Max {
			return nil, fmt.Errorf("invalid answer for %s", answer.Question)
		}
		if _, duplicate := byKey[answer.Question]; duplicate {
			return nil, fmt.Errorf("duplicate answer for %s", answer.Question)
		}
		if exact && !wanted[answer.Question] {
			return nil, fmt.Errorf("unexpected answer for %s", answer.Question)
		}
		byKey[answer.Question] = answer
	}
	for _, key := range required {
		if _, ok := byKey[key]; !ok {
			return nil, fmt.Errorf("missing classifier answer for %s", key)
		}
	}
	return byKey, nil
}
