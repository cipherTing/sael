package cli

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/cipherTing/sael/cli/internal/questions"
	"github.com/cipherTing/sael/cli/protocol"
	"github.com/cipherTing/sael/sdk"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use: "serve", Short: "Run the persistent classifier owned by a gateway process",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serveClassifier(ctx, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

func serveClassifier(parent context.Context, owner io.Reader, output io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns, transport.MaxIdleConnsPerHost = 1024, 1024
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	evaluator := &serviceEvaluator{http: client}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if r.URL.Path != "/check" || r.Method != http.MethodPost {
				http.NotFound(w, r)
				return
			}
			var input protocol.Request
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if decoder.Decode(&input) != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			selected, err := questions.Select(input.Questions)
			if err != nil {
				http.Error(w, "invalid questions", http.StatusBadRequest)
				return
			}
			deadline := input.Deadline
			if deadline.IsZero() {
				deadline = time.Now().Add(5 * time.Second)
			}
			work, stop := context.WithDeadline(r.Context(), deadline)
			defer stop()
			result := evaluator.check(work, input, selected)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		}),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	stopped := make(chan error, 1)
	go func() { stopped <- server.Serve(listener) }()
	// EOF is also delivered when the owning gateway crashes or is killed.
	go func() { _, _ = io.Copy(io.Discard, owner); cancel() }()
	err = json.NewEncoder(output).Encode(protocol.Ready{Version: protocol.Version, Address: listener.Addr().String(), Token: token})
	if err == nil {
		select {
		case <-ctx.Done():
		case err = <-stopped:
		}
	}
	cancel()
	// This private service belongs to the gateway. Once its owner disappears,
	// no caller can use a graceful response; close even incomplete requests.
	_ = server.Close()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type serviceEvaluator struct {
	mu                     sync.Mutex
	baseURL, apiKey, model string
	client                 *sdk.Client
	http                   *http.Client
}

func (e *serviceEvaluator) check(ctx context.Context, input protocol.Request, selected sdk.Questions) protocol.Response {
	if input.BaseURL == "" || input.APIKey == "" || input.Model == "" {
		return protocol.Response{Error: "unavailable"}
	}
	e.mu.Lock()
	client := e.client
	if client == nil || e.baseURL != input.BaseURL || e.apiKey != input.APIKey || e.model != input.Model {
		var err error
		client, err = sdk.New(sdk.WithBaseURL(input.BaseURL), sdk.WithAPIKey(input.APIKey), sdk.WithModel(input.Model), sdk.WithHTTPClient(e.http))
		if err != nil {
			e.mu.Unlock()
			return protocol.Response{Error: "unavailable"}
		}
		e.client, e.baseURL, e.apiKey, e.model = client, input.BaseURL, input.APIKey, input.Model
	}
	e.mu.Unlock()
	result, err := client.Evaluate(ctx, input.Text, selected)
	if err != nil {
		kind := "unavailable"
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, sdk.ErrTimeout):
			kind = "timeout"
		case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, sdk.ErrAborted):
			kind = "canceled"
		case errors.Is(err, sdk.ErrDecode):
			kind = "invalid_response"
		}
		return protocol.Response{Error: kind}
	}
	if len(result.Answers) != len(selected) {
		return protocol.Response{Error: "invalid_response"}
	}
	response := protocol.Response{Answers: make([]protocol.Answer, 0, len(selected))}
	for key, q := range selected {
		a := protocol.Answer{Question: key, Type: q.QuestionType()}
		if a.Type == "noul" {
			value, ok := result.Noul(key)
			if !ok {
				return protocol.Response{Error: "invalid_response"}
			}
			a.Value = value.Noul
		} else {
			value, ok := result.Score(key)
			if !ok {
				return protocol.Response{Error: "invalid_response"}
			}
			a.Value = value.Score
		}
		response.Answers = append(response.Answers, a)
	}
	return response
}
