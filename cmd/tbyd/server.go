package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"github.com/kalambet/tbyd/internal/api"
	"github.com/kalambet/tbyd/internal/composer"
	"github.com/kalambet/tbyd/internal/config"
	"github.com/kalambet/tbyd/internal/engine"
	"github.com/kalambet/tbyd/internal/ingest"
	"github.com/kalambet/tbyd/internal/intent"
	"github.com/kalambet/tbyd/internal/pipeline"
	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/proxy"
	"github.com/kalambet/tbyd/internal/reranking"
	"github.com/kalambet/tbyd/internal/retrieval"
	"github.com/kalambet/tbyd/internal/storage"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the tbyd server (foreground)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServer()
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running tbyd server",
	RunE: func(cmd *cobra.Command, args []string) error {
		return stopServer()
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show tbyd system status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return showStatus()
	},
}

func pidFilePath(dataDir string) string {
	return filepath.Join(dataDir, "tbyd.pid")
}

func writePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func removePIDFile(path string) {
	os.Remove(path)
}

func runServer() error {
	fmt.Fprintf(os.Stderr, "tbyd version %s\n", version)

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

	// Write PID file. Check if server is already running via health endpoint.
	pidPath := pidFilePath(cfg.Storage.DataDir)
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", cfg.Server.Port)
	healthClient := &http.Client{Timeout: 2 * time.Second}
	if resp, err := healthClient.Get(healthURL); err == nil {
		resp.Body.Close()
		if pid, pidErr := readPIDFile(pidPath); pidErr == nil {
			printWarning("tbyd is already running (PID %d)", pid)
			return fmt.Errorf("server already running (PID %d)", pid)
		}
		printWarning("tbyd is already running on port %d", cfg.Server.Port)
		return fmt.Errorf("server already running on port %d", cfg.Server.Port)
	}
	if err := writePIDFile(pidPath); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer removePIDFile(pidPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Detect and check local inference engine readiness.
	eng, err := engine.Detect(engine.DetectConfig{OllamaBaseURL: cfg.Ollama.BaseURL})
	if err != nil {
		return fmt.Errorf("detecting inference engine: %w", err)
	}
	if err := engine.EnsureReady(ctx, eng, cfg.Ollama.FastModel, cfg.Ollama.EmbedModel, os.Stderr); err != nil {
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
	ollamaEngine := eng
	extractor := intent.NewExtractor(engine.ChatAdapter(ollamaEngine), cfg.Ollama.FastModel)
	embedder := retrieval.NewEmbedder(ollamaEngine, cfg.Ollama.EmbedModel)
	vectorStore := retrieval.NewSQLiteStore(store.DB())
	retriever := retrieval.NewRetriever(embedder, vectorStore)
	profileMgr := profile.NewManager(store)
	comp := composer.New(0)
	rerankTimeout, err := time.ParseDuration(cfg.Enrichment.RerankingTimeout)
	if err != nil {
		slog.Warn("invalid reranking timeout, using default 5s", "value", cfg.Enrichment.RerankingTimeout, "error", err)
		rerankTimeout = 5 * time.Second
	}
	reranker := reranking.NewReranker(
		ollamaEngine,
		cfg.Ollama.FastModel,
		cfg.Enrichment.RerankingEnabled,
		rerankTimeout,
		cfg.Enrichment.RerankingThreshold,
		cfg.Retrieval.TopK,
	)
	enricher := pipeline.NewEnricher(extractor, retriever, profileMgr, comp, reranker, cfg.Retrieval.TopK)

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
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
		Vectors:    vectorStore,
		Retriever:  retriever,
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

	// Build and start MCP server (stdio transport in a goroutine).
	mcpEngine := &api.EngineAdapter{
		ChatFn: func(chatCtx context.Context, model string, messages []api.MCPMessage, _ *api.MCPSchema) (string, error) {
			engineMsgs := make([]engine.Message, len(messages))
			for i, m := range messages {
				engineMsgs[i] = engine.Message{Role: m.Role, Content: m.Content}
			}
			return eng.Chat(chatCtx, model, engineMsgs, nil)
		},
	}
	mcpSrv := api.NewMCPServer(api.MCPDeps{
		Store:     store,
		Profile:   profileMgr,
		Retriever: retriever,
		Engine:    mcpEngine,
		DeepModel: cfg.Ollama.DeepModel,
	})
	stdioSrv := server.NewStdioServer(mcpSrv)
	go func() {
		if err := stdioSrv.Listen(ctx, os.Stdin, os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("MCP stdio server error", "error", err)
		}
	}()
	slog.Info("MCP server started (stdio transport)")

	// Start server in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "tbyd listening on %s\n", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for signal or server error.
	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "shutting down...")
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

func stopServer() error {
	cfg, err := config.Load()
	if err != nil {
		// Try default data dir if config loading fails (e.g., missing API key).
		printError("could not load config: %v", err)
		return err
	}

	pidPath := pidFilePath(cfg.Storage.DataDir)
	pid, err := readPIDFile(pidPath)
	if err != nil {
		printError("tbyd is not running (no PID file)")
		return fmt.Errorf("not running: %w", err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		printError("could not find process %d", pid)
		return err
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		printError("could not stop tbyd (PID %d): %v", pid, err)
		removePIDFile(pidPath)
		return err
	}

	printSuccess("Sent stop signal to tbyd (PID %d)", pid)
	return nil
}

func showStatus() error {
	cfg, err := config.Load()
	if err != nil {
		// Still show partial status even if config fails.
		printError("config error: %v", err)
		return nil
	}

	// Check server health.
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port)
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(serverURL + "/health")
	if err != nil {
		printStatus("Server", "stopped")
	} else {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			printStatus("Server", "running on port %d", cfg.Server.Port)
		} else {
			printStatus("Server", "error (HTTP %d)", resp.StatusCode)
		}
	}

	// Check Ollama.
	ollamaResp, err := client.Get(cfg.Ollama.BaseURL + "/api/version")
	if err != nil {
		printStatus("Ollama", "not running")
	} else {
		ollamaResp.Body.Close()
		printStatus("Ollama", "running at %s", cfg.Ollama.BaseURL)
	}

	// Show models.
	printStatus("Fast model", "%s", cfg.Ollama.FastModel)
	printStatus("Deep model", "%s", cfg.Ollama.DeepModel)
	printStatus("Embed model", "%s", cfg.Ollama.EmbedModel)

	// Show doc/interaction counts if server is running.
	apiToken, tokenErr := config.GetAPIToken(config.NewKeychain())
	if tokenErr == nil && resp != nil && resp.StatusCode == 200 {
		docsResp, err := apiGet(client, serverURL+"/context-docs?limit=100", apiToken)
		if err == nil {
			var docs []json.RawMessage
			if json.NewDecoder(docsResp.Body).Decode(&docs) == nil {
				printStatus("Context docs", "%s", countLabel(len(docs), 100))
			}
			docsResp.Body.Close()
		}
		interResp, err2 := apiGet(client, serverURL+"/interactions?limit=100", apiToken)
		if err2 == nil {
			var interactions []json.RawMessage
			if json.NewDecoder(interResp.Body).Decode(&interactions) == nil {
				printStatus("Interactions", "%s", countLabel(len(interactions), 100))
			}
			interResp.Body.Close()
		}
	}

	printStatus("Data dir", "%s", cfg.Storage.DataDir)
	return nil
}

func countLabel(count, limit int) string {
	if count >= limit {
		return fmt.Sprintf("%d+", count)
	}
	return fmt.Sprintf("%d", count)
}

func apiGet(client *http.Client, url, token string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return client.Do(req)
}
