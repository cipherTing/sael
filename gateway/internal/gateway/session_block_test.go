package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranscriptLookupIsBoundedAt256Keys(t *testing.T) {
	messages := make([]map[string]string, maxTranscriptLookupKeys+10)
	for i := range messages {
		messages[i] = map[string]string{"role": "user", "content": "message" + string(rune('a'+i%26))}
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		t.Fatal(err)
	}
	plan := transcriptBlockPlan("credential-a", "openai_chat", body)
	if len(plan.keys) != maxTranscriptLookupKeys || !plan.lookupTruncated {
		t.Fatalf("lookup was not bounded: len=%d truncated=%v", len(plan.keys), plan.lookupTruncated)
	}
}

func TestTranscriptBlockKeysMatchAContinuationButNotAStaticFirstTurn(t *testing.T) {
	first := []byte(`{"messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"}]}`)
	continuation := []byte(`{"messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"},{"role":"assistant","content":"blocked"},{"role":"user","content":"continue"}]}`)
	other := []byte(`{"messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"different"}]}`)
	one := transcriptBlockPlan("credential-a", "openai_chat", first)
	two := transcriptBlockPlan("credential-a", "openai_chat", continuation)
	three := transcriptBlockPlan("credential-a", "openai_chat", other)
	if len(one.keys) == 0 || len(two.keys) == 0 {
		t.Fatal("transcript keys were not derived")
	}
	if !containsString(two.keys, one.keys[len(one.keys)-1]) {
		t.Fatal("continuation did not retain the blocked conversation prefix")
	}
	if containsString(three.keys, one.keys[len(one.keys)-1]) {
		t.Fatal("different latest user message reused the blocked prefix")
	}
	if one.exact == "" {
		t.Fatal("transcript plan is missing exact key")
	}
}

func TestTranscriptKeysAreIsolatedByCredentialAndSupportResponsesAndAnthropic(t *testing.T) {
	responses := []byte(`{"instructions":"shared","input":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"}]}`)
	anthropic := []byte(`{"messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"}]}`)
	a := transcriptBlockPlan("credential-a", "openai_responses", responses)
	b := transcriptBlockPlan("credential-b", "openai_responses", responses)
	if a.exact == "" || b.exact == "" || a.exact == b.exact {
		t.Fatal("responses transcript key crossed credentials")
	}
	if len(transcriptBlockPlan("credential-a", "anthropic", anthropic).keys) == 0 {
		t.Fatal("anthropic transcript was not derived")
	}
}

func TestExplicitSessionIDTakesPriorityAndIsSanitized(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	r.Header.Set("X-Session-Id", "  stable-session  ")
	if got := requestSession(r, "body-session"); got != "stable-session" {
		t.Fatalf("session=%q", got)
	}
	r.Header.Set("X-Session-Id", "bad\nvalue")
	if got := requestSession(r, "body-session"); got != "body-session" {
		t.Fatalf("invalid header should be ignored, got %q", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
