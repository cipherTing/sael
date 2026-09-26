// Command sael-gateway runs the HTTP proxy and administration UI.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cipherTing/sael/gateway/internal/classifier"
	"github.com/cipherTing/sael/gateway/internal/config"
	"github.com/cipherTing/sael/gateway/internal/gateway"
	"github.com/cipherTing/sael/gateway/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, cfg.DatabaseURL, cfg.SpoolPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if cfg.Upstream != nil {
		if err := db.SeedUpstream(ctx, cfg.Upstream.String()); err != nil {
			return err
		}
	}
	if err := db.SeedJevDefaults(ctx, cfg.JevMaxInputTokens); err != nil {
		return err
	}
	runtimeStore, err := store.OpenRedis(ctx, db, cfg.RedisURL)
	if err != nil {
		return err
	}
	defer runtimeStore.Close()
	direct := classifier.NewSDK()
	defer direct.Close()
	var detector gateway.Classifier = direct
	if cfg.CLIPath != "" {
		detector = classifier.CLI{Path: cfg.CLIPath}
	}
	api := gateway.New(runtimeStore, detector, cfg.AdminPassword)
	defer api.Close()
	api.Timeout = cfg.Timeout
	api.MaxBodyBytes = cfg.MaxRequestBodySize
	api.AsyncReviewConcurrency = cfg.AsyncReviewConcurrency
	api.TrustedProxies = cfg.TrustedProxies
	api.IngressAddress, api.PublicIngressURL = cfg.Listen, cfg.PublicIngressURL
	servers := []*http.Server{
		{Addr: cfg.AdminListen, Handler: webHandler(api.AdminHandler(), cfg.WebDir), ReadHeaderTimeout: cfg.ReadHeaderTimeout, MaxHeaderBytes: cfg.MaxHeaderBytes, IdleTimeout: cfg.IdleTimeout},
		{Addr: cfg.Listen, Handler: api, ReadHeaderTimeout: cfg.ReadHeaderTimeout, MaxHeaderBytes: cfg.MaxHeaderBytes, IdleTimeout: cfg.IdleTimeout},
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		var lastPrune time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				work, cancel := context.WithTimeout(ctx, 10*time.Second)
				if err := db.Replay(work); err != nil {
					slog.Error("local spool replay failed", "error", err)
				}
				if p, err := runtimeStore.Policy(work); err == nil {
					if err := runtimeStore.PruneTrustedKeys(work, p.TrustedKeyIdle()); err != nil {
						slog.Warn("trusted credential cleanup failed", "error", err)
					}
				}
				if time.Since(lastPrune) >= 24*time.Hour {
					if p, err := db.Policy(work); err == nil {
						days := 0
						if p.RetentionDays != nil {
							days = *p.RetentionDays
						}
						if err := db.Prune(work, days); err != nil {
							slog.Error("event pruning failed", "error", err)
						} else {
							lastPrune = time.Now()
						}
					}
				}
				cancel()
			}
		}
	}()
	slog.Info("gateway listening", "admin", cfg.AdminListen, "ingress", cfg.Listen)
	return serve(ctx, servers)
}

func serve(ctx context.Context, servers []*http.Server) error {
	listeners := make([]net.Listener, 0, len(servers))
	for _, server := range servers {
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", server.Addr)
		if err != nil {
			for _, open := range listeners {
				_ = open.Close()
			}
			return err
		}
		listeners = append(listeners, listener)
	}
	errorsCh := make(chan error, len(servers))
	for i, server := range servers {
		go func() { errorsCh <- server.Serve(listeners[i]) }()
	}
	var result error
	select {
	case <-ctx.Done():
	case result = <-errorsCh:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
		}
	}
	if errors.Is(result, http.ErrServerClosed) {
		return nil
	}
	return result
}

func webHandler(api http.Handler, dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/admin/") {
			api.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1beta/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("ok"))
			return
		}
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		name := filepath.Join(dir, strings.TrimPrefix(filepath.Clean(r.URL.Path), "/"))
		if info, err := os.Stat(name); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}
