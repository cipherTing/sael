//go:build !windows

package classifier

import (
	"context"
	"github.com/cipherTing/sael/gateway/internal/gateway"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCLIReadsJSONAndSendsTextOnStdin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sael")
	script := "#!/bin/sh\nread line\n[ \"$line\" = \"current text\" ] || exit 3\n[ \"$1\" = check ] && [ \"$2\" = --json ] || exit 4\nprintf '[{\"question\":\"cyber_abuse\",\"type\":\"noul\",\"value\":0.9}]'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := (CLI{Path: path}).Check(context.Background(), "current text")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Question != "cyber_abuse" || got[0].Value != 0.9 {
		t.Fatalf("got %+v", got)
	}
}

func TestCLITimeoutStopsProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sael")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := (CLI{Path: path}).Check(ctx, "text"); err == nil || time.Since(start) > time.Second {
		t.Fatalf("timeout not respected: %v", err)
	}
}

func TestCLIUsesSavedJevSettingsInsteadOfProcessEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sael")
	script := "#!/bin/sh\n[ \"$TYPESAFE_API_KEY\" = saved-key ] || exit 3\n[ \"$TYPESAFE_BASE_URL\" = https://saved.example/v1 ] || exit 4\n[ \"$TYPESAFE_DEFAULT_MODEL\" = jev-saved ] || exit 5\nprintf '[{\"question\":\"cyber_abuse\",\"type\":\"noul\",\"value\":0.9}]'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_API_KEY", "wrong-process-key")
	_, err := (CLI{Path: path}).CheckConfigured(context.Background(), "text", gateway.JevConfig{BaseURL: "https://saved.example/v1", Model: "jev-saved", APIKey: "saved-key"})
	if err != nil {
		t.Fatal(err)
	}
}
