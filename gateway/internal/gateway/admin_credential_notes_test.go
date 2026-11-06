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

type credentialNoteTestStore struct {
	*testStore
	note string
	err  error
}

func (s *credentialNoteTestStore) SetCredentialNote(_ context.Context, id, note string) error {
	if id != "known" {
		return ErrNotFound
	}
	if s.err != nil {
		return s.err
	}
	s.note = note
	return nil
}

func TestTrustedKeyNoteUpdatesAndClearsInline(t *testing.T) {
	s, base, _ := makeServer(t, activePolicy(), &testClassifier{})
	store := &credentialNoteTestStore{testStore: base}
	s.Store = store
	for _, input := range []string{"  生产环境 🔑  ", ""} {
		body, err := json.Marshal(map[string]string{"note": input})
		if err != nil {
			t.Fatal(err)
		}
		w := adminRequest(t, s, http.MethodPatch, "/admin/trusted-keys/known", string(body))
		var got struct {
			Note string `json:"note"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.Note != strings.TrimSpace(input) || store.note != got.Note {
			t.Fatalf("save note: status=%d note=%q store=%q body=%s err=%v", w.Code, got.Note, store.note, w.Body, err)
		}
	}
}

func TestTrustedKeyNoteReturnsTheNormalizedSavedValue(t *testing.T) {
	s, base, _ := makeServer(t, activePolicy(), &testClassifier{})
	store := &credentialNoteTestStore{testStore: base}
	s.Store = store
	w := adminRequest(t, s, http.MethodPatch, "/admin/trusted-keys/known", `{"note":"\u0000头\u0000尾\u0000"}`)
	var got struct {
		Note string `json:"note"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.Note != "头 尾" || store.note != "头 尾" {
		t.Fatalf("normalized note mismatch: status=%d response=%q saved=%q err=%v", w.Code, got.Note, store.note, err)
	}
}

func TestTrustedKeyNoteRejectsInvalidInputsWithoutChangingSavedValue(t *testing.T) {
	s, base, _ := makeServer(t, activePolicy(), &testClassifier{})
	store := &credentialNoteTestStore{testStore: base, note: "saved"}
	s.Store = store
	for _, body := range []string{`{}`, `{"note":null}`, `{"note":12}`, `{"note":"line\nbreak"}`, `{"note":"line\rbreak"}`, `{"note":"line\u0085break"}`, `{"note":"line\u2028break"}`, `{"note":"line\u2029break"}`, `{"note":"ok","extra":true}`, `{"note":"ok"} {}`, `{"note":"` + strings.Repeat("甲", 241) + `"}`} {
		w := adminRequest(t, s, http.MethodPatch, "/admin/trusted-keys/known", body)
		if w.Code != http.StatusBadRequest || store.note != "saved" {
			t.Fatalf("invalid note accepted: status=%d saved=%q body=%s", w.Code, store.note, body)
		}
	}
	valid := `{"note":"` + strings.Repeat("甲", 240) + `"}`
	if w := adminRequest(t, s, http.MethodPatch, "/admin/trusted-keys/known", valid); w.Code != http.StatusOK {
		t.Fatalf("240 Unicode characters rejected: status=%d body=%s", w.Code, w.Body)
	}
}

func TestTrustedKeyNoteReportsMissingStorageAndRequiresAdminSession(t *testing.T) {
	s, base, _ := makeServer(t, activePolicy(), &testClassifier{})
	store := &credentialNoteTestStore{testStore: base}
	s.Store = store
	if w := adminRequest(t, s, http.MethodPatch, "/admin/trusted-keys/missing", `{"note":"x"}`); w.Code != http.StatusNotFound {
		t.Fatalf("missing credential status=%d body=%s", w.Code, w.Body)
	}
	store.err = errors.New("database unavailable")
	if w := adminRequest(t, s, http.MethodPatch, "/admin/trusted-keys/known", `{"note":"x"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("storage failure status=%d body=%s", w.Code, w.Body)
	}
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, authorizedRequest(http.MethodPatch, "/admin/trusted-keys/known", strings.NewReader(`{"note":"x"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated note update status=%d", w.Code)
	}
}

type filteredCredentialTestStore struct {
	*testStore
	filter TrustedCredentialFilter
}

func (s *filteredCredentialTestStore) TrustedCredentials(_ context.Context, _ time.Duration, filter TrustedCredentialFilter) (TrustedCredentials, error) {
	s.filter = filter
	return TrustedCredentials{Items: []TrustedCredential{{ID: "known", Note: "生产环境"}}, Total: 1}, nil
}

func TestTrustedKeysAPIForwardsTrimmedFiltersAndReturnsNotes(t *testing.T) {
	s, base, _ := makeServer(t, activePolicy(), &testClassifier{})
	store := &filteredCredentialTestStore{testStore: base}
	s.Store = store
	w := adminRequest(t, s, http.MethodGet, "/admin/trusted-keys?key_prefix=%20sk-Ab%20&note=%20Ops%20&offset=50&limit=10", "")
	var got TrustedCredentials
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("list response status=%d body=%s err=%v", w.Code, w.Body, err)
	}
	if store.filter != (TrustedCredentialFilter{KeyPrefix: "sk-Ab", Note: "Ops", Offset: 50, Limit: 10}) || len(got.Items) != 1 || got.Items[0].Note != "生产环境" {
		t.Fatalf("wrong filtered response: filter=%+v list=%+v", store.filter, got)
	}
}
