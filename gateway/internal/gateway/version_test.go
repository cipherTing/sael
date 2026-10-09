package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cipherTing/sael/gateway/internal/version"
)

func TestVersionRequiresAdminLoginAndReportsGatewayBuild(t *testing.T) {
	s, _, _ := makeServer(t, activePolicy(), &testClassifier{})
	request := httptest.NewRequest(http.MethodGet, "/admin/version", http.NoBody)
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, request)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("version accessible without login: %d", w.Code)
	}
	request.AddCookie(login(t, s))
	w = httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, request)
	if w.Code != http.StatusOK {
		t.Fatalf("version status %d: %s", w.Code, w.Body.String())
	}
	var result version.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.CurrentVersion != "devel" || result.UpdateAvailable != nil {
		t.Fatalf("incorrect unversioned build: %+v", result)
	}
}
