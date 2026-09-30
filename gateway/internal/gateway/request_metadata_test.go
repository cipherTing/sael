package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestRecordCapturesInboundSizeFormatAndCurrentInputLength(t *testing.T) {
	p := activePolicy()
	p.Scenes[0].Action = policy.Allow
	s, store, _ := makeServer(t, p, &testClassifier{answers: fullAnswers(map[string]float64{"cyber_abuse": .9})})
	raw := `{"model":"target","input":[{"role":"user","content":"旧的用户输入"},{"role":"assistant","content":"历史回复"},{"role":"user","content":"你好🙂"}]}`
	r := authorizedRequest("POST", "/v1/responses", strings.NewReader(raw))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	r.ContentLength = 999999
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	s.reviewWG.Wait()
	if len(store.events) != 1 || w.Code != 201 || w.Header().Get("X-Upstream-Body") != raw {
		t.Fatal("request metadata logging changed the forwarded body or lost the record")
	}
	data, err := json.Marshal(store.events[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		ContentType  string `json:"content_type"`
		RequestBytes int64  `json:"request_bytes"`
		InputChars   int    `json:"input_chars"`
	}
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields.ContentType != "application/json" || fields.RequestBytes != int64(len(raw)) || fields.InputChars != 3 {
		t.Fatalf("inbound metadata = %+v, want JSON, %d bytes and 3 current-input runes", fields, len(raw))
	}
}

func TestImageOptionMetadataIsRedactedBeforePersistence(t *testing.T) {
	var event Event
	if err := json.Unmarshal([]byte(`{"parameters":{"image_size":"password=private-size","image_quality":"api_key=private-quality","image_output_format":"Bearer private-format"}}`), &event); err != nil {
		t.Fatal(err)
	}
	event.Redact()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-") {
		t.Fatalf("image options leaked sensitive values: %s", data)
	}
}
