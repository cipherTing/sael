package privacy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactTextMasksCredentialsAndPIIButPreservesMeaning(t *testing.T) {
	cases := []struct {
		input   string
		secrets []string
	}{
		{`请查 user@example.com 13800138000 的记录，血腥分数 1.5`, []string{"user@example.com", "13800138000"}},
		{`password="a secret with spaces" api_key='a quoted key' access_token=raw-token&max_tokens=500 Bearer raw-auth`, []string{"a secret with spaces", "a quoted key", "raw-token", "raw-auth"}},
		{`{"password":"private\"escaped", "nested":[{"refresh_token":"raw-refresh","prompt":"email user@example.com"}],"max_tokens":500}`, []string{`private`, "raw-refresh", "user@example.com"}},
		{`API_KEY: abcdefgh authorization: Basic dXNlcjpwYXNz sk-12345678901234567890`, []string{"abcdefgh", "dXNlcjpwYXNz", "sk-12345678901234567890"}},
		{"-----BEGIN PRIVATE KEY-----\nprivate-pem\n-----END PRIVATE KEY-----", []string{"private-pem"}},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := RedactText(tc.input)
			for _, secret := range tc.secrets {
				if strings.Contains(got, secret) {
					t.Errorf("leaked %q in %q", secret, got)
				}
			}
			if RedactText(got) != got {
				t.Errorf("redaction is not idempotent: %q", got)
			}
			if json.Valid([]byte(tc.input)) && !json.Valid([]byte(got)) {
				t.Fatalf("invalid redacted JSON: %s", got)
			}
		})
	}
	if got := RedactText("血腥程度 1.5，自伤 0.8；max_tokens=500，模型 gpt-6-luna"); got != "血腥程度 1.5，自伤 0.8；max_tokens=500，模型 gpt-6-luna" {
		t.Fatal(got)
	}
}

func TestRedactionFastChecksPreservePatternBehavior(t *testing.T) {
	cases := []string{
		"ordinary user text with no personal information", "中文内容没有私密信息", "ſecret=private", "api_Key=private", "AUTHORIZATION=bearer private",
		"GOCSPX-abcdefghijklmnopqrstuvwxyz", "ghp_abcdefghijklmnopqrstuv", "eyJhbGci.eyJzdWI.signature", "AIza01234567890123456789012345678901234",
		"client-secret : tokenValue", "code_verifier=secret-value", "set-cookie=private-value", "secretaccesskey=secret-value", "ACCESSKEYID=secret-value",
		"bAsIc dXNlcjpzZWNyZXQ=", "-----BEGIN RSA PRIVATE KEY-----\nprivate\n-----END RSA PRIVATE KEY-----", "someone@EXAMPLE.COM", "110101199001012345", "+86 13800138000",
	}
	for _, text := range cases {
		want := text
		for _, re := range secrets {
			want = re.ReplaceAllString(want, hidden)
		}
		want = credentials.ReplaceAllString(want, `${1}`+hidden)
		if got := redactPlain(text); got != want {
			t.Errorf("changed detection for %q: %q != %q", text, got, want)
		}
	}
}
