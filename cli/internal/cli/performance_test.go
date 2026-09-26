package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cipherTing/sael/cli/internal/questions"
	"github.com/cipherTing/sael/sdk"
)

// Reports local HTTP cost and real TCP connection count, excluding model inference.
func BenchmarkClassifierInvocation(b *testing.B) {
	var connections atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fakeResponse)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()
	b.Run("reused_sdk", func(b *testing.B) {
		c, err := sdk.New(sdk.WithBaseURL(srv.URL), sdk.WithAPIKey("benchmark-only"))
		if err != nil {
			b.Fatal(err)
		}
		q := questions.Moderation()
		start := connections.Load()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := c.Evaluate(context.Background(), "ordinary user input", q); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(connections.Load()-start)/float64(b.N), "connections/op")
	})
	b.Run("cli_process", func(b *testing.B) {
		path := os.Getenv("SAEL_PERF_CLI_PATH")
		if path == "" {
			b.Skip("SAEL_PERF_CLI_PATH not set")
		}
		env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "SAEL_HOME=" + b.TempDir(), "TYPESAFE_API_KEY=benchmark-only", "TYPESAFE_BASE_URL=" + srv.URL}
		start := connections.Load()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cmd := exec.Command(path, "check", "--json")
			cmd.Env = env
			cmd.Stdin = strings.NewReader("ordinary user input")
			if out, err := cmd.CombinedOutput(); err != nil {
				b.Fatalf("%v: %s", err, out)
			}
		}
		b.ReportMetric(float64(connections.Load()-start)/float64(b.N), "connections/op")
	})
}
