package classifier

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestSDKUsesSavedConnectionAndPreservesMeasurements(t *testing.T) {
	calls := 0
	extraAnswer := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			State, Model string
			Questions    map[string]json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/systemone" || body.State != "current user prompt" || body.Model != "saved-model" || len(body.Questions) != 11 || r.Header.Get("Authorization") != "Bearer saved-key" {
			t.Error("changed classifier request")
		}
		answers := map[string]any{}
		for _, q := range policy.Questions {
			if q.Type == "score" {
				answers[q.Key] = map[string]any{"type": "score", "score": 1.5}
			} else {
				answers[q.Key] = map[string]any{"type": "noul", "noul": .2}
			}
		}
		if extraAnswer {
			answers["unexpected"] = map[string]any{"type": "noul", "noul": .2}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer upstream.Close()
	c := NewSDK()
	for i := 0; i < 2; i++ {
		a, err := c.CheckConfigured(context.Background(), "current user prompt", gateway.JevConfig{BaseURL: upstream.URL + "/v1", APIKey: "saved-key", Model: "saved-model"})
		if err != nil {
			t.Fatal(err)
		}
		if err := policy.ValidateAnswers(a); err != nil {
			t.Fatal(err)
		}
		for _, v := range a {
			if v.Type == "score" && v.Value != 1.5 {
				t.Fatal("rounded fractional score")
			}
		}
	}
	if calls != 2 {
		t.Fatal("unexpected retries")
	}
	extraAnswer = true
	if _, err := c.CheckConfigured(context.Background(), "current user prompt", gateway.JevConfig{BaseURL: upstream.URL + "/v1", APIKey: "saved-key", Model: "saved-model"}); !errors.Is(err, gateway.ErrInvalidClassifierResponse) {
		t.Fatal("unexpected answer silently ignored")
	}
}
func TestSDKPropagatesCancellationAndInvalidAnswer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"answers":{}}`)) }))
	defer upstream.Close()
	c := NewSDK()
	cfg := gateway.JevConfig{BaseURL: upstream.URL, APIKey: "key", Model: "model"}
	if _, err := c.CheckConfigured(context.Background(), "text", cfg); !errors.Is(err, gateway.ErrInvalidClassifierResponse) {
		t.Fatal("missing answers not classified", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := c.CheckConfigured(ctx, "text", cfg); err == nil {
		t.Fatal("expired request sent")
	}
}
