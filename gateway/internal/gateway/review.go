package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
	"unicode/utf8"

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
	decision := policy.Decision{Action: policy.Allow}
	required := policy.RequiredQuestions(p, event.Protocol, event.Model)
	if len(required) == 0 {
		count.Outcome = "clean"
		return count, decision
	}
	config, configErr := s.Store.Jev(parent)
	inputLimit := config.inputLimit()
	inputTokens, tokenErr := inputTokensOverLimit(event.Text, inputLimit)
	if tokenErr != nil {
		slog.Warn("input token estimate failed", "request_id", event.RequestID, "error", tokenErr)
	}
	if tokenErr == nil && inputTokens > inputLimit {
		count.Outcome = "input_too_long"
		event.Kind, event.ErrorKind = "warning", "classifier_input_too_long"
		event.InputChars, event.InputTokens, event.JevInputLimit = utf8.RuneCountInString(event.Text), inputTokens, inputLimit
		event.Decision = decision
		event.TextPreview = preview(event.Text, 500)
		event.Text = ""
		s.writeEvent(parent, event)
		return count, decision
	}
	keys := []string{}
	byScene := map[string]string{}
	cacheAvailable := false
	if s.ReviewCache != nil && configErr == nil {
		for _, scene := range p.Scenes {
			if scene.AppliesTo(event.Protocol) && scene.AppliesToModel(event.Model) {
				key := sceneCacheKey(config, scene, event.Text)
				keys = append(keys, key)
				byScene[scene.ID] = key
			}
		}
		count.CacheLookup = len(keys) > 0
		values, err := s.ReviewCache.Lookup(parent, keys)
		cacheAvailable = err == nil
		if err == nil && len(keys) > 0 {
			verdicts := map[string]bool{}
			complete := true
			for id, key := range byScene {
				value, ok := values[key]
				if !ok {
					complete = false
					break
				}
				verdicts[id] = value
			}
			if complete {
				count.CacheHit = true
				event.ReviewSource = "cache"
				event.Decision, event.Trace = cachedDecision(p, verdicts, event.Protocol, event.Model)
				return s.finishReview(parent, event, p, count)
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
		return count, decision
	}
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	s.markClassifier(err, timedOut)
	count.ClassifierSample = true
	count.ClassifierMS = time.Since(started).Milliseconds()
	if err == nil && decision.SceneID == "" {
		count.Outcome = "clean"
		return count, decision
	}
	event.ReviewSource = "jev"
	event.ClassifierMS = count.ClassifierMS
	event.Scores, event.Decision, event.Trace = scores, decision, trace
	if err != nil {
		event.Kind, event.ErrorKind = "failure", classifierErrorKind(err, timedOut)
		count.ErrorKind = event.ErrorKind
		count.Outcome = "unreviewed"
		s.writeEvent(parent, event)
		return count, decision
	}
	if s.ReviewCache != nil && cacheAvailable && len(keys) > 0 {
		values := map[string]bool{}
		matched := map[string]bool{}
		for _, t := range trace {
			matched[t.ID] = t.Status == "effective" || t.Status == "shadowed"
		}
		// Cache every applicable scene predicate once the prompt has at least one
		// hit. False values are meaningful here: without them a later lookup can
		// never prove that all current scenes were evaluated.
		if decision.SceneID != "" {
			for _, scene := range p.Scenes {
				if key, ok := byScene[scene.ID]; ok {
					values[key] = matched[scene.ID]
				}
			}
		}
		if err := s.ReviewCache.Save(parent, values); err != nil {
			slog.Warn("review cache write failed", "error", err)
		}
	}
	count.Scores = scores
	return s.finishReview(parent, event, p, count)
}

func (s *Server) finishReview(ctx context.Context, event Event, p policy.Policy, count Count) (Count, policy.Decision) {
	decision := event.Decision
	if decision.SceneID == "" {
		count.Outcome = "clean"
		return count, decision
	}
	for _, scene := range p.Scenes {
		if scene.ID == decision.SceneID || slices.Contains(decision.AlsoMatched, scene.ID) {
			count.SceneMatches = append(count.SceneMatches, SceneMatch{SceneID: scene.ID, Name: scene.Name, Action: string(scene.Action), WinnerID: decision.SceneID, WinnerName: decision.SceneName})
		}
	}
	event.Kind = "hit"
	s.writeEvent(ctx, event)
	count.Outcome = "hit_allowed"
	if decision.Action == policy.Block {
		count.Outcome = "blocked"
	}
	return count, decision
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
		result, decision := s.review(s.reviewContext, event, p, count)
		s.recordCount(result)
		if p.SessionBlockOnNonblockingReview && decision.SceneID != "" {
			ttl := time.Duration(p.SessionBlockTTLSeconds) * time.Second
			if ttl <= 0 {
				ttl = time.Hour
			}
			s.rememberSessionBlock(s.reviewContext, blockPlan, ttl)
		}
	}()
	return true
}
