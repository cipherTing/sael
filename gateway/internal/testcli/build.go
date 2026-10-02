// Package testcli builds the real CLI for gateway integration and load tests.
package testcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Binary returns a CLI executable scoped to the calling test's temporary files.
func Binary(t testing.TB) string {
	t.Helper()
	if path := os.Getenv("SAEL_TEST_CLI_PATH"); path != "" {
		info, err := os.Stat(path) // #nosec G703 -- Explicit test-runner executable path, never provided by a gateway request.
		if err != nil || info.IsDir() {
			t.Fatalf("SAEL_TEST_CLI_PATH is not an executable file: %s", path)
		}
		return path
	}
	name := "sael"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	_, file, _, _ := runtime.Caller(0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", path, "./cmd/sael") // #nosec G204 -- Test-owned output path and fixed build target.
	cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..", "..", "cli")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return path
}
