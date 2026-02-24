package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kalambet/tbyd/internal/api"
	"github.com/kalambet/tbyd/internal/composer"
	"github.com/kalambet/tbyd/internal/config"
	"github.com/kalambet/tbyd/internal/engine"
	"github.com/kalambet/tbyd/internal/ingest"
	"github.com/kalambet/tbyd/internal/intent"
	"github.com/kalambet/tbyd/internal/pipeline"
	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/proxy"
	"github.com/kalambet/tbyd/internal/retrieval"
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

	// Initialize structured logging.
	logLevel := slog.LevelInfo
	if strings.EqualFold(cfg.Log.Level, "debug") {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	// Ensure API token exists in platform secret store.
	if _, err := config.GetAPIToken(config.NewKeychain()); err != nil {
		return fmt.Errorf("initializing API token: %w", err)
	}
	slog.Info("API bearer token available")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Detect and check local inference engine readiness.
	eng, err := engine.Detect(engine.DetectConfig{OllamaBaseURL: cfg.Ollama.BaseURL})
	if err != nil {
		return fmt.Errorf("detecting inference engine: %w", err)
	}
	if err := engine.EnsureReady(ctx, eng, cfg.Ollama.FastModel, cfg.Ollama.EmbedModel, os.Stdout); err != nil {
		return err
	}

	// Open storage.
	store, err := storage.Open(cfg.Storage.DataDir)
	if err != nil {
		return fmt.Errorf("opening storage: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: closing storage: %v\n", err)
		}
	}()

	// Build enrichment pipeline.
	ollamaEngine := eng // engine.Engine backed by Ollama
	extractor := intent.NewExtractor(engine.ChatAdapter(ollamaEngine), cfg.Ollama.FastModel)
	embedder := retrieval.NewEmbedder(ollamaEngine, cfg.Ollama.EmbedModel)
	vectorStore := retrieval.NewSQLiteStore(store.DB())
	retriever := retrieval.NewRetriever(embedder, vectorStore)
	profileMgr := profile.NewManager(store)
	comp := composer.New(0) // default 4000 tokens
	enricher := pipeline.NewEnricher(extractor, retriever, profileMgr, comp, cfg.Retrieval.TopK)

	// Retrieve API token for bearer auth on management endpoints.
	apiToken, err := config.GetAPIToken(config.NewKeychain())
	if err != nil {
		return fmt.Errorf("getting API token: %w", err)
	}

	// Build HTTP handler and server.
	proxyClient := proxy.NewClient(cfg.Proxy.OpenRouterAPIKey)
	openaiHandler := api.NewOpenAIHandler(proxyClient, enricher)
	appHandler := api.NewAppHandler(api.AppDeps{
		Store:      store,
		Profile:    profileMgr,
		Token:      apiToken,
		HTTPClient: http.DefaultClient,
	})

	// Compose top-level router: OpenAI-compat routes + management/ingest routes.
	topRouter := chi.NewRouter()
	topRouter.Mount("/", openaiHandler)
	topRouter.Mount("/", appHandler)

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: topRouter,
	}

	// Start ingest worker.
	worker := ingest.NewWorker(store, embedder, vectorStore, 500*time.Millisecond)
	go worker.Run(ctx)

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
