package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

func TestRiskSourcesAggregateOnlyMatchingHitsAndMaskKeys(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.pool.Exec(ctx, "TRUNCATE audit_events"); err != nil {
		t.Fatal(err)
	}
	master := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if err := p.InitCredentials(ctx, master); err != nil {
		t.Fatal(err)
	}
	if err := p.saveCredential(ctx, "risk-key-a", "https://relay.example", "abcdefghijklmnopqrstuvwx"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	for i, event := range []gateway.Event{
		{Kind: "hit", CredentialID: "risk-key-a", ClientIP: "203.0.113.1", Protocol: "openai_images_generations", Model: "m"},
		{Kind: "hit", CredentialID: "risk-key-a", ClientIP: "203.0.113.1", Protocol: "openai_images_edits", Model: "m"},
		{Kind: "hit", CredentialID: "risk-key-b", ClientIP: "203.0.113.2", Protocol: "openai_images_generations", Model: "m"},
		{Kind: "failure", CredentialID: "risk-key-a", ClientIP: "203.0.113.1", Protocol: "openai_images_generations", Model: "m"},
		{Kind: "warning", CredentialID: "risk-key-a", ClientIP: "203.0.113.1", Protocol: "openai_images_generations", Model: "m"},
		{Kind: "hit", CredentialID: "risk-key-a", ClientIP: "203.0.113.1", Protocol: "openai_chat", Model: "m"},
		{Kind: "hit", CredentialID: "risk-key-a", ClientIP: "203.0.113.1", Protocol: "openai_images_generations", Model: "other"},
		{Kind: "hit", CredentialID: "risk-key-a", ClientIP: "203.0.113.1", Protocol: "openai_images_generations", Model: "m", Time: now.Add(time.Hour)},
	} {
		event.ID = "risk-source-" + string(rune('a'+i))
		event.RequestID = event.ID
		if event.Time.IsZero() {
			event.Time = now
		}
		event.Decision = policy.Decision{Action: policy.Block}
		event.Text = strings.Repeat("large current user input ", 1000)
		if err := p.WriteEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	server := gateway.New(p, nil, "test-password")
	server.Security = &adminTestSecurity{}
	defer server.Close()
	login := httptest.NewRecorder()
	server.AdminHandler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(`{"password":"test-password"}`)))
	request := httptest.NewRequest(http.MethodGet, "/admin/risk-sources?start="+now.Format(time.RFC3339)+"&end="+now.Add(time.Minute).Format(time.RFC3339)+"&endpoint=openai_images&model=m", http.NoBody)
	request.AddCookie(login.Result().Cookies()[0])
	w := httptest.NewRecorder()
	if reader, ok := any(p).(gateway.RiskSourceReader); ok {
		if _, err := reader.RiskSources(ctx, gateway.AnalyticsFilter{Since: now, Until: now.Add(time.Minute), Endpoint: "openai_images", Model: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	server.AdminHandler().ServeHTTP(w, request)
	var result struct {
		Keys []struct {
			CredentialID string `json:"credential_id"`
			MaskedKey    string `json:"masked_key"`
			Count        int64  `json:"count"`
		} `json:"keys"`
		IPs []struct {
			ClientIP string `json:"client_ip"`
			Count    int64  `json:"count"`
		} `json:"ips"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); w.Code != http.StatusOK || err != nil {
		t.Fatalf("risk API: %d %s %v", w.Code, w.Body.String(), err)
	}
	if len(result.Keys) != 2 || result.Keys[0].CredentialID != "risk-key-a" || result.Keys[0].Count != 2 || result.Keys[0].MaskedKey != "abcdef******stuvwx" || result.Keys[1].Count != 1 {
		t.Fatalf("wrong key ranking: %+v", result.Keys)
	}
	if len(result.IPs) != 2 || result.IPs[0].ClientIP != "203.0.113.1" || result.IPs[0].Count != 2 {
		t.Fatalf("wrong IP ranking: %+v", result.IPs)
	}
	if strings.Contains(w.Body.String(), "abcdefghijklmnopqrstuvwx") || strings.Contains(w.Body.String(), "large current") {
		t.Fatal("ranking leaked a key or prompt")
	}
	// Startup migrations can be replayed without discarding the prior records.
	reopened, err := Open(ctx, dsn, filepath.Join(t.TempDir(), "reopen"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var n int
	if err := reopened.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE kind='hit'").Scan(&n); err != nil || n != 6 {
		t.Fatalf("migration replay lost records: %d %v", n, err)
	}
}
