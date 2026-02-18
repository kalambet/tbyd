package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kalambet/tbyd/internal/api"
	"github.com/kalambet/tbyd/internal/config"
	"github.com/kalambet/tbyd/internal/ollama"
	"github.com/kalambet/tbyd/internal/proxy"
	"github.com/kalambet/tbyd/internal/storage"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Fprintf(os.Stdout, "tbyd version %s\n", version)

	// Load config.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Check Ollama readiness.
	ollamaClient := ollama.New(cfg.Ollama.BaseURL)
	if err := ollama.EnsureReady(ctx, ollamaClient, cfg.Ollama.FastModel, cfg.Ollama.EmbedModel, os.Stdout); err != nil {
		return err
	}

	// Open storage.
	store, err := storage.Open(cfg.Storage.DataDir)
	if err != nil {
		return fmt.Errorf("opening storage: %w", err)
	}
	defer store.Close()

	// Build HTTP handler and server.
	proxyClient := proxy.NewClient(cfg.Proxy.OpenRouterAPIKey)
	handler := api.NewOpenAIHandler(proxyClient)

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	// Start server in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stdout, "tbyd listening on %s\n", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for signal or server error.
	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stdout, "shutting down...")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
	}

	// Graceful shutdown with timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
