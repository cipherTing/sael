package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestEventAPIDefaultsToBoundedPreviewWithoutReturningFullText(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{})
	text := strings.Repeat("中", 600)
	store.events = []Event{{ID: "long-event", Time: time.Now(), Text: text, TextPreview: "old preview"}}
	for _, path := range []string{"/admin/events", "/admin/events/long-event"} {
		w := adminRequest(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var item map[string]any
		if path == "/admin/events" {
			var items []map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil || len(items) != 1 {
				t.Fatalf("list: %s %v", w.Body.String(), err)
			}
			item = items[0]
		} else if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
			t.Fatal(err)
		}
		if _, hasText := item["text"]; hasText {
			t.Fatalf("%s returned full text by default", path)
		}
		if item["text_preview"] != strings.Repeat("中", 500) || item["text_available"] != true || item["text_chars"] != float64(600) {
			t.Fatalf("%s returned wrong preview metadata: %+v", path, item)
		}
	}
	if store.events[0].Text != text {
		t.Fatal("preparing the response discarded the stored full text")
	}
}

func TestEventFullTextRequiresLoginAndReturnsOnlyStoredText(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{})
	text := strings.Repeat("文", 700)
	store.events = []Event{{ID: "full", Text: text}, {ID: "old", TextPreview: "historical preview only"}}
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/admin/events/full/text", http.NoBody))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("full text without session: %d", w.Code)
	}
	w = adminRequest(t, s, http.MethodGet, "/admin/events/full/text", "")
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || len(result) != 1 || result["text"] != text {
		t.Fatalf("full text: %d %s %v", w.Code, w.Body.String(), err)
	}
	for _, path := range []string{"/admin/events/old/text", "/admin/events/missing/text"} {
		w = adminRequest(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d, must not pass off a preview as full text", path, w.Code)
		}
	}
}

func TestPreviewOnlyHistoryDeclaresThatFullTextIsUnavailable(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{})
	store.events = []Event{{ID: "old", TextPreview: strings.Repeat("旧", 700)}}
	w := adminRequest(t, s, http.MethodGet, "/admin/events/old", "")
	var item map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || item["text_available"] != false || item["text_chars"] != float64(0) || item["text_preview"] != strings.Repeat("旧", 500) {
		t.Fatalf("historical preview: %d %+v", w.Code, item)
	}
}

func TestOversizedInputKeepsRedactedFullTextForExplicitReading(t *testing.T) {
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{})
	store.jev.MaxInputChars = 3
	text := strings.Repeat("中", 600) + " password=never-store user@example.com"
	raw, _ := json.Marshal(map[string]string{"input": text})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(raw))))
	if w.Code != http.StatusCreated || len(store.events) != 1 {
		t.Fatalf("input warning: %d events=%d", w.Code, len(store.events))
	}
	e := store.events[0]
	wantText := strings.Repeat("中", 600) + " password=[已隐藏] [已隐藏]"
	if e.Kind != "warning" || e.Text != wantText || e.InputChars != utf8.RuneCountInString(text) {
		t.Fatalf("warning lost redacted full text or original input count: kind=%s len=%d chars=%d", e.Kind, utf8.RuneCountInString(e.Text), e.InputChars)
	}
	w = adminRequest(t, s, http.MethodGet, "/admin/events/"+e.ID+"/text", "")
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || result.Text != wantText {
		t.Fatalf("warning full text: %d %s %v", w.Code, w.Body.String(), err)
	}
}
