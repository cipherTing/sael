// Package classifier adapts the existing Sael CLI to the gateway.
package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

// CLI invokes the configured Sael executable for one current user text.
type CLI struct{ Path string }

// Check sends text on standard input and parses the CLI JSON measurements.
func (c CLI) Check(ctx context.Context, currentText string) ([]policy.Answer, error) {
	return c.run(ctx, currentText, nil)
}

// CheckConfigured runs the CLI with the saved Jev connection for this request.
func (c CLI) CheckConfigured(ctx context.Context, currentText string, settings gateway.JevConfig) ([]policy.Answer, error) {
	return c.run(ctx, currentText, &settings)
}

func (c CLI) run(ctx context.Context, currentText string, settings *gateway.JevConfig) ([]policy.Answer, error) {
	path := c.Path
	if path == "" {
		path = "sael"
	}
	// #nosec G204 -- Path is supplied by the gateway operator, never by an HTTP request.
	cmd := exec.CommandContext(ctx, path, "check", "--json")
	if settings != nil {
		env := make([]string, 0, len(os.Environ())+3)
		for _, item := range os.Environ() {
			if !strings.HasPrefix(item, "TYPESAFE_API_KEY=") && !strings.HasPrefix(item, "TYPESAFE_BASE_URL=") && !strings.HasPrefix(item, "TYPESAFE_DEFAULT_MODEL=") {
				env = append(env, item)
			}
		}
		cmd.Env = append(env, "TYPESAFE_API_KEY="+settings.APIKey, "TYPESAFE_BASE_URL="+settings.BaseURL, "TYPESAFE_DEFAULT_MODEL="+settings.Model)
	}
	cmd.Stdin = strings.NewReader(currentText)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sael check: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var answers []policy.Answer
	if err := json.Unmarshal(out.Bytes(), &answers); err != nil {
		return nil, fmt.Errorf("sael JSON: %w", err)
	}
	return answers, nil
}
