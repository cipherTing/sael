package gateway

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPolicyUpdatePreservesInvalidFieldError(t *testing.T) {
	update := PolicyUpdate{Fields: map[string]json.RawMessage{
		"enabled": json.RawMessage(`"yes"`),
	}}
	_, err := update.Apply(activePolicy())
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("missing invalid-policy category: %v", err)
	}
	var fieldError *json.UnmarshalTypeError
	if !errors.As(err, &fieldError) || fieldError.Field != "enabled" {
		t.Fatalf("lost invalid field cause: %v", err)
	}
}
