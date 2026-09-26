package classifier

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/sdk"
	"github.com/cipherTing/sael/sdk/moderation"
)

// SDK keeps the gateway's classification calls in process and shares HTTP connections.
type SDK struct {
	mu        sync.Mutex
	settings  gateway.JevConfig
	client    *sdk.Client
	http      *http.Client
	questions sdk.Questions
}

// NewSDK constructs the shared HTTP client and moderation catalog.
func NewSDK() *SDK {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 1024
	transport.MaxIdleConnsPerHost = 1024
	return &SDK{http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, questions: moderation.Questions()}
}

// Close releases idle classifier connections.
func (c *SDK) Close() { c.http.CloseIdleConnections() }

// Check requires the saved connection supplied through CheckConfigured.
func (c *SDK) Check(context.Context, string) ([]policy.Answer, error) {
	return nil, errors.New("saved classifier configuration is required")
}

// CheckConfigured evaluates text using the saved connection and shared catalog.
func (c *SDK) CheckConfigured(ctx context.Context, text string, settings gateway.JevConfig) ([]policy.Answer, error) {
	c.mu.Lock()
	client := c.client
	if client == nil || c.settings.BaseURL != settings.BaseURL || c.settings.APIKey != settings.APIKey || c.settings.Model != settings.Model {
		var err error
		client, err = sdk.New(sdk.WithBaseURL(settings.BaseURL), sdk.WithAPIKey(settings.APIKey), sdk.WithModel(settings.Model), sdk.WithHTTPClient(c.http))
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		c.client, c.settings = client, settings
	}
	c.mu.Unlock()
	result, err := client.Evaluate(ctx, text, c.questions)
	if err != nil {
		if errors.Is(err, sdk.ErrDecode) {
			return nil, fmt.Errorf("%w: %w", gateway.ErrInvalidClassifierResponse, err)
		}
		return nil, err
	}
	if len(result.Answers) != len(policy.Questions) {
		return nil, gateway.ErrInvalidClassifierResponse
	}
	answers := make([]policy.Answer, 0, len(policy.Questions))
	for _, q := range policy.Questions {
		answer := policy.Answer{Question: q.Key, Type: q.Type}
		if q.Type == "noul" {
			value, ok := result.Noul(q.Key)
			if !ok {
				return nil, gateway.ErrInvalidClassifierResponse
			}
			answer.Value = value.Noul
		} else {
			value, ok := result.Score(q.Key)
			if !ok {
				return nil, gateway.ErrInvalidClassifierResponse
			}
			answer.Value = value.Score
		}
		answers = append(answers, answer)
	}
	return answers, nil
}
