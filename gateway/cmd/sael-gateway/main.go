// Command sael-gateway runs the HTTP proxy and administration UI.
package main

import (
	"context"
	"errors"
	"log/slog"
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
	api := gateway.New(db, classifier.CLI{Path: cfg.CLIPath}, cfg.AdminPassword)
	api.Timeout = cfg.Timeout
	api.Slots = make(chan struct{}, cfg.Concurrency)
	server := &http.Server{Addr: cfg.Listen, Handler: webHandler(api, cfg.WebDir), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
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
				if time.Since(lastPrune) >= 24*time.Hour {
					if p, err := db.Policy(work); err == nil && p.RetentionDays != nil {
						if err := db.Prune(work, *p.RetentionDays); err != nil {
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
	slog.Info("gateway listening", "address", cfg.Listen)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func webHandler(api http.Handler, dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/admin/") || strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1beta/") {
			api.ServeHTTP(w, r)
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
