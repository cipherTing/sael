package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionPlanDefersHistoryParsing(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", http.NoBody)
	plan := sessionPlanForRequest(r, "credential", "192.0.2.1", []byte(`{"messages":[{"role":"user","content":"hello"}]}`), "openai_chat")
	if len(plan.transcript.keys) != 0 {
		t.Fatal("request preparation parsed history before checking the risk scope")
	}
}

func TestHistoryLookupSkipsUnactivatedScope(t *testing.T) {
	store := &batchSessionStore{testStore: &testStore{blocked: map[string]time.Time{}}}
	s := New(store, nil, "secret")
	defer s.Close()
	plan := sessionBlockPlan{transcript: transcriptPlan{scope: "not-activated"}, derive: func() transcriptPlan {
		t.Fatal("normal request computed historical fingerprints")
		return transcriptPlan{}
	}}
	if key, _ := s.sessionBlocked(context.Background(), plan); key != "" || len(store.queries) != 0 {
		t.Fatal("normal request queried historical conversation keys")
	}
}

func TestHistoryIdentityIsComputedOncePerRequest(t *testing.T) {
	body := []byte(`{"input":"first"}`)
	r := httptest.NewRequest("POST", "/v1/responses", http.NoBody)
	plan := sessionPlanForRequest(r, "credential", "192.0.2.1", body, "openai_responses")
	first := plan.history()
	copy(body, []byte(`{"input":"other"}`))
	if got := plan.history(); got.exact != first.exact || got.exact == "" {
		t.Fatal("one request recomputed its transcript identity")
	}
}

type batchSessionStore struct {
	*testStore
	queries       [][]string
	explicitError bool
	scopeChecks   int
}

func (s *batchSessionStore) FindSessionBlock(_ context.Context, keys []string, _ time.Time) (string, error) {
	s.queries = append(s.queries, append([]string(nil), keys...))
	if s.explicitError {
		return "", errors.New("Redis unavailable")
	}
	for _, key := range keys {
		if s.blocked[key].After(time.Now()) {
			return key, nil
		}
	}
	return "", nil
}

func (s *batchSessionStore) SessionBlockActive(ctx context.Context, key string, now time.Time) (bool, error) {
	s.scopeChecks++
	return s.testStore.SessionBlockActive(ctx, key, now)
}

func TestSessionHistoryUsesBatchLookupAndKeepsEarliestMatch(t *testing.T) {
	store := &batchSessionStore{testStore: &testStore{blocked: map[string]time.Time{"scope": time.Now().Add(time.Hour), "second": time.Now().Add(time.Hour), "third": time.Now().Add(time.Hour)}}}
	s := New(store, nil, "secret")
	defer s.Close()
	plan := sessionBlockPlan{transcript: transcriptPlan{scope: "scope", keys: []string{"first", "second", "third"}}}
	key, source := s.sessionBlocked(context.Background(), plan)
	if key != "second" || source != "history" {
		t.Fatalf("matched %q/%q", key, source)
	}
	if len(store.queries) != 1 || store.scopeChecks != 1 {
		t.Fatalf("history was not batch-read: batches=%d scope checks=%d", len(store.queries), store.scopeChecks)
	}
}

func TestSessionLookupFailureStopsBeforeHistory(t *testing.T) {
	store := &batchSessionStore{testStore: &testStore{blocked: map[string]time.Time{"scope": time.Now().Add(time.Hour), "history": time.Now().Add(time.Hour)}}, explicitError: true}
	s := New(store, nil, "secret")
	defer s.Close()
	key, _ := s.sessionBlocked(context.Background(), sessionBlockPlan{explicit: "explicit", transcript: transcriptPlan{scope: "scope", keys: []string{"history"}}})
	if key != "" || store.scopeChecks != 0 {
		t.Fatalf("failed explicit read proceeded into history: key=%q scope checks=%d", key, store.scopeChecks)
	}
}

func TestResponsesStringInputSupportsHistoryFreeze(t *testing.T) {
	plan := transcriptBlockPlan("credential", "openai_responses", []byte(`{"input":"hello"}`))
	if plan.exact == "" || len(plan.keys) != 1 {
		t.Fatal("Responses string input cannot be frozen by its transcript")
	}
}

func TestSessionScopeIncludesEmptyUserAgent(t *testing.T) {
	if sessionScopeKey("credential", "192.0.2.1", "") == "" {
		t.Fatal("clients without User-Agent cannot use transcript freezing")
	}
}

func TestUserAgentVersionChangesKeepRiskScope(t *testing.T) {
	for _, versions := range [][2]string{{"Codex CLI 1.2.3", "Codex CLI 1.2.4"}, {"client/1.0", "client/1.9"}} {
		if sessionScopeKey("credential", "192.0.2.1", versions[0]) != sessionScopeKey("credential", "192.0.2.1", versions[1]) {
			t.Fatalf("client version update escaped its risk scope: %v", versions)
		}
	}
}

func TestBlankInstructionsDoNotChangeHistoryIdentity(t *testing.T) {
	base := transcriptBlockPlan("credential", "openai_responses", []byte(`{"input":[{"role":"user","content":"hello"}]}`))
	for _, instructions := range []string{`""`, `"   "`, `"\n\t"`} {
		body := []byte(`{"instructions":` + instructions + `,"input":[{"role":"user","content":"hello"}]}`)
		if transcriptBlockPlan("credential", "openai_responses", body).exact != base.exact {
			t.Fatalf("blank instructions changed conversation identity: %s", instructions)
		}
	}
}

func TestNullInstructionsRemainPartOfHistoryIdentity(t *testing.T) {
	base := transcriptBlockPlan("credential", "openai_responses", []byte(`{"input":"hello"}`))
	withNull := transcriptBlockPlan("credential", "openai_responses", []byte(`{"instructions":null,"input":"hello"}`))
	if withNull.exact == base.exact {
		t.Fatal("null instructions were treated as a blank string")
	}
}

func TestBodyConversationStringIsAnExplicitSession(t *testing.T) {
	if got := bodySessionID("openai_responses", []byte(`{"conversation":"conv_123","input":"hello"}`)); got != "conv_123" {
		t.Fatalf("conversation string not recognized: %q", got)
	}
}

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
