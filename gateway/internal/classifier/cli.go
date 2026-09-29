// Package classifier connects gateway reviews to a persistent CLI subprocess.
package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/cipherTing/sael/cli/protocol"
	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/policy"
)

// CLI owns the classification process for the gateway lifetime.
type CLI struct {
	cmd            *exec.Cmd
	owner          io.WriteCloser
	client         *http.Client
	address, token string
	done           chan struct{}
	cancel         context.CancelFunc
	closeOnce      sync.Once
}

// NewCLI starts the classifier before the gateway accepts requests.
func NewCLI(ctx context.Context, path string) (*CLI, error) {
	life, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(life, path, "serve") // #nosec G204 -- Executable comes from deployment configuration, never ingress input.
	cmd.Stderr = os.Stderr
	cmd.WaitDelay = time.Second
	owner, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = owner.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = owner.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start classifier CLI: %w", err)
	}
	type startup struct {
		ready protocol.Ready
		err   error
	}
	decoded := make(chan startup, 1)
	go func() {
		var result startup
		result.err = json.NewDecoder(io.LimitReader(stdout, 4096)).Decode(&result.ready)
		_ = stdout.Close()
		decoded <- result
	}()
	wait, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	var result startup
	select {
	case result = <-decoded:
	case <-wait.Done():
		cancel()
		_ = stdout.Close()
		result = <-decoded
		result.err = wait.Err()
	}
	host, port, addressErr := net.SplitHostPort(result.ready.Address)
	number, portErr := strconv.Atoi(port)
	if result.err != nil || addressErr != nil || portErr != nil || host != "127.0.0.1" || number < 1 || number > 65535 || result.ready.Version != protocol.Version || len(result.ready.Token) < 32 {
		cancel()
		_ = owner.Close()
		_ = cmd.Wait()
		return nil, errors.New("classifier CLI failed to become ready")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns, transport.MaxIdleConnsPerHost = 1024, 1024
	c := &CLI{cmd: cmd, owner: owner, address: result.ready.Address, token: result.ready.Token, cancel: cancel, done: make(chan struct{}),
		client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	go func() { _ = cmd.Wait(); close(c.done) }()
	return c, nil
}

// Close terminates the owned process.
func (c *CLI) Close() {
	c.closeOnce.Do(func() {
		_ = c.owner.Close()
		select {
		case <-c.done:
		case <-time.After(4 * time.Second):
			c.cancel()
			<-c.done
		}
		c.cancel()
		c.client.CloseIdleConnections()
	})
}

// Done closes when the owned CLI exits, including unexpected process termination.
func (c *CLI) Done() <-chan struct{} { return c.done }

// Check requires a saved connection snapshot supplied through CheckConfigured.
func (*CLI) Check(context.Context, string) ([]policy.Answer, error) {
	return nil, errors.New("saved classifier configuration is required")
}

// CheckConfigured evaluates a request using its saved configuration snapshot.
func (c *CLI) CheckConfigured(parent context.Context, text string, settings gateway.JevConfig, questions []string) ([]policy.Answer, error) {
	select {
	case <-c.done:
		return nil, errors.New("classifier CLI unavailable")
	default:
	}
	timeout := 5 * time.Second
	if settings.TimeoutMS > 0 {
		timeout = time.Duration(settings.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	raw, err := json.Marshal(protocol.Request{Text: text, Questions: questions, BaseURL: settings.BaseURL, APIKey: settings.APIKey, Model: settings.Model, Deadline: deadline}) // #nosec G117 -- Credentials travel only over the authenticated loopback channel to the owned CLI, never to logs or storage.
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.address+"/check", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("classifier CLI unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("classifier CLI unavailable")
	}
	var result protocol.Response
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, gateway.ErrInvalidClassifierResponse
	}
	switch result.Error {
	case "":
	case "invalid_response":
		return nil, gateway.ErrInvalidClassifierResponse
	case "timeout":
		return nil, context.DeadlineExceeded
	case "canceled":
		return nil, context.Canceled
	default:
		return nil, errors.New("classifier unavailable")
	}
	answers := make([]policy.Answer, 0, len(result.Answers))
	for _, a := range result.Answers {
		answers = append(answers, policy.Answer{Question: a.Question, Type: a.Type, Value: a.Value})
	}
	if policy.ValidateAnswersFor(answers, questions) != nil {
		return nil, gateway.ErrInvalidClassifierResponse
	}
	return answers, nil
}
