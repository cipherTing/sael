package gateway

import (
	"net/http"
	"testing"
)

func TestRiskSourcesRequireLoginAndRejectInvalidTimeRange(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	w := adminRequest(t, s, http.MethodGet, "/admin/risk-sources?start=invalid&end=invalid", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid range: %d %s", w.Code, w.Body.String())
	}
}

func TestRiskSourcesUnavailableReturnsServiceError(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	w := adminRequest(t, s, http.MethodGet, "/admin/risk-sources", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported source: %d %s", w.Code, w.Body.String())
	}
}
