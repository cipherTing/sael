package gateway

import (
	"context"
	"net/http"
	"testing"
)

type keySamplingTestStore struct {
	*testStore
	rate int
}

func (s *keySamplingTestStore) SetKeySampleRate(_ context.Context, id string, rate int) error {
	if id != "fingerprint" {
		return ErrNotFound
	}
	s.rate = rate
	return nil
}

func (s *keySamplingTestStore) KeySampleRate(_ context.Context, id string) (int, error) {
	if id == "fingerprint" {
		return s.rate, nil
	}
	return 100, nil
}

func TestKeySamplingAdminUpdatesTrustedKeyRate(t *testing.T) {
	base := &testStore{policy: activePolicy()}
	store := &keySamplingTestStore{testStore: base, rate: 100}
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	s.Store = store
	updated := adminRequest(t, s, http.MethodPatch, "/admin/key-sampling/fingerprint", `{"sample_rate":0}`)
	if updated.Code != http.StatusOK || store.rate != 0 {
		t.Fatalf("zero rate update: %d %d %s", updated.Code, store.rate, updated.Body.String())
	}
	updated = adminRequest(t, s, http.MethodPatch, "/admin/key-sampling/fingerprint", `{"sample_rate":83}`)
	if updated.Code != http.StatusOK || store.rate != 83 {
		t.Fatalf("percentage rate update: %d %d %s", updated.Code, store.rate, updated.Body.String())
	}
}

func TestKeySamplingAdminRejectsInvalidRates(t *testing.T) {
	base := &testStore{policy: activePolicy(), upstream: UpstreamConfig{BaseURL: "https://relay.example"}}
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	s.Store = &keySamplingTestStore{testStore: base, rate: 100}
	for _, body := range []string{`{"sample_rate":-1}`, `{"sample_rate":101}`, `{"sample_rate":50.5}`, `{}`} {
		if got := adminRequest(t, s, http.MethodPatch, "/admin/key-sampling/fingerprint", body).Code; got != http.StatusBadRequest {
			t.Fatalf("body %s status=%d, want 400", body, got)
		}
	}
}

type eventsFilterTestStore struct {
	*testStore
	filter EventFilter
}

func (s *eventsFilterTestStore) Events(_ context.Context, filter EventFilter) ([]Event, error) {
	s.filter = filter
	return nil, nil
}

func TestEventsAPIForwardsUnreviewedVisibilityToStorageFilter(t *testing.T) {
	base := &testStore{policy: activePolicy()}
	store := &eventsFilterTestStore{testStore: base}
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	s.Store = store
	shown := adminRequest(t, s, http.MethodGet, "/admin/events?kind=hit&include_unreviewed=true", "")
	if shown.Code != http.StatusOK || !store.filter.IncludeUnreviewed || store.filter.Kind != "hit" {
		t.Fatalf("visible unreviewed filter was not forwarded: %d %+v %s", shown.Code, store.filter, shown.Body.String())
	}
	hidden := adminRequest(t, s, http.MethodGet, "/admin/events?kind=hit", "")
	if hidden.Code != http.StatusOK || store.filter.IncludeUnreviewed {
		t.Fatalf("default filter included unreviewed rows: %d %+v", hidden.Code, store.filter)
	}
	bad := adminRequest(t, s, http.MethodGet, "/admin/events?include_unreviewed=perhaps", "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid visibility value status=%d", bad.Code)
	}
}
