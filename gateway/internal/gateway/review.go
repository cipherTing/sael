package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

func (s *Server) recordCount(c Count) {
	if !c.UserInput {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Store.Increment(ctx, c); err != nil {
		slog.Error("record count failed", "error", err)
	}
}
func (s *Server) observeIngress(ctx context.Context, c Count) {
	if !c.UserInput {
		return
	}
	if sampler, ok := s.Store.(interface {
		ObserveIngress(context.Context, Count) error
	}); ok {
		if err := sampler.ObserveIngress(ctx, c); err != nil {
			slog.Error("record ingress rate failed", "error", err)
		}
	}
}

// review owns classification and persistence; it never writes to the client's response.
func (s *Server) review(parent context.Context, event Event, p policy.Policy, count Count) (Count, policy.Decision) {
	event.ExecutionMode = policy.Blocking
	result, decision, _ := s.reviewWithEvent(parent, event, p, count, true)
	return result, decision
}

// reviewWithEvent shares the decision engine while letting callers own persistence.
func (s *Server) reviewWithEvent(parent context.Context, event Event, p policy.Policy, count Count, record bool) (Count, policy.Decision, Event) {
	decision := policy.Decision{Action: policy.Allow}
	required := policy.RequiredQuestions(p, event.Protocol, event.Model)
	if len(required) == 0 {
		count.Outcome = "clean"
		return count, decision, event
	}
	config, configErr := s.Store.Jev(parent)
	inputLimit := config.inputLimit()
	inputChars, overLimit := inputCharsOverLimit(event.Text, inputLimit)
	if overLimit {
		count.Outcome = "input_too_long"
		event.Kind, event.ErrorKind = "warning", "classifier_input_too_long"
		event.InputChars, event.JevInputLimit = inputChars, inputLimit
		event.Decision = decision
		event.TextPreview = preview(event.Text, 500)
		if record {
			s.writeEvent(parent, event)
		}
		return count, decision, event
	}
	keys := []string{}
	byCondition := map[policy.ConditionKey]string{}
	cacheAvailable := false
	if s.ReviewCache != nil && configErr == nil {
		for _, scene := range p.Scenes {
			if scene.AppliesTo(event.Protocol) && scene.AppliesToModel(event.Model) {
				for _, condition := range scene.Conditions {
					if _, exists := byCondition[condition.Key()]; exists {
						continue
					}
					key := conditionCacheKey(config, condition, event.Text)
					keys = append(keys, key)
					byCondition[condition.Key()] = key
				}
			}
		}
		count.CacheLookup = len(keys) > 0
		values, err := s.ReviewCache.Lookup(parent, keys)
		cacheAvailable = err == nil
		if err == nil && len(keys) > 0 {
			verdicts := map[policy.ConditionKey]bool{}
			complete := true
			for condition, key := range byCondition {
				value, ok := values[key]
				if !ok {
					complete = false
					break
				}
				verdicts[condition] = value
			}
			if complete {
				event.Decision, event.Trace, err = policy.EvaluatePredicates(p, verdicts, event.Protocol, event.Model)
				if err == nil {
					count.CacheHit = true
					event.ReviewSource = "cache"
					return s.finishReview(parent, event, p, count, record)
				}
			}
		}
	}
	timeout := s.Timeout
	if configErr == nil {
		timeout = config.timeout()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	started := time.Now()
	var scores []policy.Answer
	err := configErr
	if err == nil {
		if configured, ok := s.Classifier.(configuredClassifier); ok {
			if err = config.validate(); err == nil {
				scores, err = configured.CheckConfigured(ctx, event.Text, config, required)
			}
		} else {
			scores, err = s.Classifier.Check(ctx, event.Text)
		}
	}
	count.JevMS = time.Since(started).Milliseconds()
	var trace []policy.SceneTrace
	if err == nil {
		decision, trace, err = policy.EvaluateDetailed(p, scores, event.Protocol, event.Model)
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrInvalidClassifierResponse, err)
			decision = policy.Decision{Action: policy.Allow}
		}
	}
	if errors.Is(parent.Err(), context.Canceled) {
		count.Outcome = "client_canceled"
		return count, decision, event
	}
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	s.markClassifier(err, timedOut)
	count.ClassifierSample = true
	count.ClassifierMS = time.Since(started).Milliseconds()
	if err == nil && decision.SceneID == "" {
		event.Scores, event.Decision, event.Trace = scores, decision, trace
		count.Outcome = "clean"
		return count, decision, event
	}
	event.ReviewSource = "jev"
	event.ClassifierMS = count.ClassifierMS
	event.Scores, event.Decision, event.Trace = scores, decision, trace
	if err != nil {
		event.Kind, event.ErrorKind = "failure", classifierErrorKind(err, timedOut)
		count.ErrorKind = event.ErrorKind
		count.Outcome = "unreviewed"
		if record {
			s.writeEvent(parent, event)
		}
		return count, decision, event
	}
	if s.ReviewCache != nil && cacheAvailable && decision.SceneID != "" {
		byQuestion := map[string]float64{}
		for _, score := range scores {
			byQuestion[score.Question] = score.Value
		}
		values := map[string]bool{}
		// A winning scene enables retention of all applicable condition facts,
		// including false facts and conditions in later, unevaluated scenes.
		for condition, key := range byCondition {
			values[key] = byQuestion[condition.Question] > condition.Threshold
		}
		if err := s.ReviewCache.Save(parent, values); err != nil {
			slog.Warn("review cache write failed", "error", err)
		}
	}
	count.Scores = scores
	return s.finishReview(parent, event, p, count, record)
}

func (s *Server) finishReview(ctx context.Context, event Event, p policy.Policy, count Count, record bool) (Count, policy.Decision, Event) {
	decision := event.Decision
	if decision.SceneID == "" {
		count.Outcome = "clean"
		return count, decision, event
	}
	for _, scene := range p.Scenes {
		if scene.ID == decision.SceneID {
			count.SceneMatches = append(count.SceneMatches, SceneMatch{SceneID: scene.ID, Name: scene.Name, Action: string(decision.Action), WinnerID: decision.SceneID, WinnerName: decision.SceneName})
		}
	}
	event.Kind = "hit"
	if record {
		s.writeEvent(ctx, event)
	}
	count.Outcome = "hit_allowed"
	if decision.Action == policy.Block {
		count.Outcome = "blocked"
	}
	return count, decision, event
}

func (s *Server) startReview(event Event, p policy.Policy, count Count, blockPlan sessionBlockPlan) bool {
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	if s.closed {
		return false
	}
	if s.reviewActive >= s.AsyncReviewConcurrency {
		return false
	}
	s.reviewActive++
	s.reviewWG.Add(1)
	go func() {
		defer s.reviewWG.Done()
		defer func() { s.reviewMu.Lock(); s.reviewActive--; s.reviewMu.Unlock() }()
		event.ExecutionMode = policy.NonBlocking
		result, decision, _ := s.reviewWithEvent(s.reviewContext, event, p, count, true)
		s.recordCount(result)
		if ttl := winningSceneFreezeTTL(p, decision); ttl > 0 {
			s.rememberSessionBlock(s.reviewContext, blockPlan, ttl)
		}
	}()
	return true
}

func winningSceneFreezeTTL(p policy.Policy, decision policy.Decision) time.Duration {
	if decision.Action != policy.Block {
		return 0
	}
	for _, scene := range p.Scenes {
		if scene.ID == decision.SceneID && scene.Active() && scene.SessionBlockEnabled {
			return time.Duration(scene.SessionBlockTTLSeconds) * time.Second
		}
	}
	return 0
}
