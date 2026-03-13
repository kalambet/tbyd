package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kalambet/tbyd/internal/pipeline"
	"github.com/kalambet/tbyd/internal/proxy"
	"github.com/kalambet/tbyd/internal/storage"
)

const maxRequestBodySize = 1 << 20        // 1MB
const maxCaptureBytesStreaming = 1 << 20  // 1MB cap for accumulated streaming content
const maxResponseBodyBytes = 10 << 20    // 10MB cap for non-streaming response body

// InteractionSaver persists interactions and enqueues summarization jobs.
// All methods accept a context so that implementations can use context-aware
// database calls (e.g. ExecContext), enabling graceful cancellation on shutdown.
type InteractionSaver interface {
	SaveInteraction(ctx context.Context, i storage.Interaction) error
	EnqueueJob(ctx context.Context, job storage.Job) error
}

// interactionRecord holds all data needed to persist an interaction.
type interactionRecord struct {
	UserQuery      string
	EnrichedPrompt string
	Model          string
	CloudResponse  string
	Status         string // "completed" or "aborted"
}

// interactionSaveLoop drains the save channel until ctx is cancelled,
// then drains any remaining buffered interactions before returning.
// Runs in a single goroutine to avoid unbounded goroutine spawning.
func interactionSaveLoop(ctx context.Context, saver InteractionSaver, ch <-chan interactionRecord, enqueueSummarize bool) {
	for {
		select {
		case <-ctx.Done():
			// Drain buffered interactions on shutdown using a background
			// context so the database calls are not already cancelled.
			drainCtx := context.Background()
			for {
				select {
				case rec := <-ch:
					doSaveInteraction(drainCtx, saver, rec, enqueueSummarize)
				default:
					return
				}
			}
		case rec, ok := <-ch:
			if !ok {
				return
			}
			doSaveInteraction(ctx, saver, rec, enqueueSummarize)
		}
	}
}

// NewOpenAIHandler returns an http.Handler implementing the OpenAI-compatible
// REST API and a cleanup function the caller must invoke after the HTTP server
// has stopped accepting requests. The cleanup function blocks until the
// background interaction-save goroutine has finished draining, ensuring all
// buffered saves complete before the store is closed.
//
// When enricher is non-nil, incoming chat requests are enriched before
// forwarding to the cloud proxy. Passing nil disables enrichment (passthrough
// mode). When saver is non-nil and saveInteractions is true, completed
// interactions are persisted and queued for summarization.
//
// appCtx controls the lifetime of the background save goroutine and must
// outlive the server's request-handling lifetime. Pass context.Background()
// in tests or when save is disabled.
//
// onboarding is optional; pass nil to disable the onboarding prompt. Notify
// is called once during handler setup. The sync.Once inside the notifier
// ensures the check-and-print logic runs at most once per process lifetime,
// making it safe even if the handler were created multiple times.
func NewOpenAIHandler(appCtx context.Context, p *proxy.Client, enricher *pipeline.Enricher, saver InteractionSaver, saveInteractions bool, enqueueSummarize bool, onboarding *OnboardingNotifier) (http.Handler, func()) {
	r := chi.NewRouter()

	// Call onboarding once at setup time, not on every request.
	onboarding.Notify(os.Stderr)

	// Start a bounded save channel and single consumer goroutine.
	var saveCh chan interactionRecord
	var droppedInteractions atomic.Int64
	var saveLoopDone chan struct{}
	if saveInteractions && saver != nil {
		saveCh = make(chan interactionRecord, 64)
		saveLoopDone = make(chan struct{})
		go func() {
			defer close(saveLoopDone)
			interactionSaveLoop(appCtx, saver, saveCh, enqueueSummarize)
		}()
	}

	cleanup := func() {
		if saveLoopDone != nil {
			<-saveLoopDone
		}
	}

	r.Get("/health", handleHealth(&droppedInteractions))
	r.Get("/v1/models", handleModels(p))
	r.Post("/v1/chat/completions", handleChatCompletions(p, enricher, saveCh, &droppedInteractions))

	return r, cleanup
}

func handleHealth(droppedInteractions *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":               "ok",
			"dropped_interactions": droppedInteractions.Load(),
		})
	}
}

func handleModels(p *proxy.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		models, err := p.ListModels(r.Context())
		if err != nil {
			httpError(w, http.StatusBadGateway, "api_error", "failed to list models: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(proxy.ModelList{
			Object: "list",
			Data:   models,
		})
	}
}

func handleChatCompletions(p *proxy.Client, enricher *pipeline.Enricher, saveCh chan<- interactionRecord, droppedInteractions *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
		defer r.Body.Close()

		var req proxy.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body: %v", err)
			return
		}

		if !hasMessages(req.Messages) {
			httpError(w, http.StatusBadRequest, "invalid_request_error", "messages is required and must not be empty")
			return
		}

		// Capture original user query before enrichment.
		userQuery := extractLastUserMessage(req.Messages)

		// Enrich if enricher is available.
		if enricher != nil {
			enriched, meta := enricher.Enrich(r.Context(), req)
			req = enriched
			slog.Debug("request enriched",
				"intent_extracted", meta.IntentExtracted,
				"chunks_used", len(meta.ChunksUsed),
				"duration_ms", meta.EnrichmentDurationMs,
			)
		}

		// Always capture the final forwarded messages for interaction storage,
		// whether enriched or original (passthrough mode).
		var enrichedPrompt string
		if b, err := json.Marshal(req.Messages); err == nil {
			enrichedPrompt = string(b)
		}

		rc, err := p.Chat(r.Context(), req)
		if err != nil {
			httpError(w, http.StatusBadGateway, "api_error", "upstream error: %v", err)
			return
		}
		defer rc.Close()

		var responseBody string
		var upstreamModel string
		status := "completed"
		if req.Stream {
			var streamOK bool
			responseBody, upstreamModel, streamOK = streamResponseCapture(w, rc)
			if !streamOK {
				status = "aborted"
			}
		} else {
			body, err := io.ReadAll(io.LimitReader(rc, maxResponseBodyBytes))
			if err != nil {
				httpError(w, http.StatusBadGateway, "api_error", "reading upstream response: %v", err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			responseBody = string(body)
			// Extract model from upstream response.
			var respObj struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(body, &respObj) == nil && respObj.Model != "" {
				upstreamModel = respObj.Model
			}
		}

		// Prefer upstream model (what was actually used) over request model.
		model := upstreamModel
		if model == "" {
			model = req.Model
		}

		// Enqueue interaction save via bounded channel (non-blocking).
		if saveCh != nil && responseBody != "" {
			rec := interactionRecord{
				UserQuery:      userQuery,
				EnrichedPrompt: enrichedPrompt,
				Model:          model,
				CloudResponse:  responseBody,
				Status:         status,
			}
			select {
			case saveCh <- rec:
			default:
				n := droppedInteractions.Add(1)
				slog.Warn("interaction save channel full, dropping interaction",
					"dropped_total", n,
				)
			}
		}
	}
}

// extractLastUserMessage returns the content of the last user message from the messages array.
// Handles both string content and multi-part content arrays (vision/multi-modal).
func extractLastUserMessage(raw json.RawMessage) string {
	var msgs []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		// Try string content first.
		var s string
		if err := json.Unmarshal(msgs[i].Content, &s); err == nil {
			return s
		}
		// Try array-of-parts (multi-modal).
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(msgs[i].Content, &parts); err == nil {
			var texts []string
			for _, p := range parts {
				if p.Type == "text" && p.Text != "" {
					texts = append(texts, p.Text)
				}
			}
			return strings.Join(texts, " ")
		}
		// Unparseable content format — try earlier user messages.
		continue
	}
	return ""
}

// doSaveInteraction persists an interaction and optionally enqueues a summarization job.
func doSaveInteraction(ctx context.Context, saver InteractionSaver, rec interactionRecord, enqueueSummarize bool) {
	interactionID := uuid.New().String()
	status := rec.Status
	if status == "" {
		status = "completed"
	}
	interaction := storage.Interaction{
		ID:             interactionID,
		CreatedAt:      time.Now().UTC(),
		UserQuery:      rec.UserQuery,
		EnrichedPrompt: rec.EnrichedPrompt,
		CloudModel:     rec.Model,
		CloudResponse:  rec.CloudResponse,
		Status:         status,
		VectorIDs:      "[]",
	}

	if err := saver.SaveInteraction(ctx, interaction); err != nil {
		slog.Error("failed to save interaction",
			"error", err,
			"ephemeral_id", interactionID,
			"model", rec.Model,
		)
		return
	}

	if !enqueueSummarize || status != "completed" {
		return
	}

	payload, err := json.Marshal(map[string]string{"interaction_id": interactionID})
	if err != nil {
		slog.Error("failed to marshal summarize job payload", "error", err, "interaction_id", interactionID)
		return
	}

	job := storage.Job{
		ID:          uuid.New().String(),
		Type:        "interaction_summarize",
		PayloadJSON: string(payload),
	}
	if err := saver.EnqueueJob(ctx, job); err != nil {
		slog.Error("failed to enqueue summarize job", "error", err, "interaction_id", interactionID)
	}
}

// streamResponseCapture streams SSE events to the client while reassembling
// the assistant's content from streaming delta chunks. Returns the reassembled
// content as a synthetic non-streaming response JSON for storage, the upstream
// model name extracted from SSE chunks, and whether the stream completed
// successfully (received [DONE]). An incomplete stream returns false so the
// caller can mark the interaction as aborted.
func streamResponseCapture(w http.ResponseWriter, rc io.Reader) (string, string, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "api_error", "streaming not supported")
		return "", "", false
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var contentBuilder strings.Builder
	var streamModel string
	streamDone := false

	reader := bufio.NewReader(rc)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			w.Write(line)
			flusher.Flush()

			// Parse SSE data lines to extract delta content.
			trimmed := strings.TrimSpace(string(line))
			if strings.HasPrefix(trimmed, "data: ") {
				data := strings.TrimPrefix(trimmed, "data: ")
				if data == "[DONE]" {
					streamDone = true
				} else {
					var chunk struct {
						Model   string `json:"model"`
						Choices []struct {
							Delta struct {
								Content string `json:"content"`
							} `json:"delta"`
						} `json:"choices"`
					}
					if json.Unmarshal([]byte(data), &chunk) == nil {
						if streamModel == "" && chunk.Model != "" {
							streamModel = chunk.Model
						}
						if contentBuilder.Len() < maxCaptureBytesStreaming {
							for _, c := range chunk.Choices {
								contentBuilder.WriteString(c.Delta.Content)
							}
						}
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				slog.Error("upstream stream read error", "error", err)
				errPayload, marshalErr := json.Marshal(map[string]any{
					"error": map[string]any{
						"message": "upstream read error",
						"type":    "server_error",
					},
				})
				if marshalErr == nil {
					fmt.Fprintf(w, "data: %s\n\n", errPayload)
					flusher.Flush()
				} else {
					slog.Error("failed to marshal stream error payload", "error", marshalErr)
				}
			}
			break
		}
	}

	// Build a synthetic non-streaming response for storage so that
	// extractAssistantContent can parse it uniformly.
	assembled := contentBuilder.String()
	if assembled == "" {
		return "", streamModel, streamDone
	}
	synth, err := json.Marshal(map[string]any{
		"model": streamModel,
		"choices": []map[string]any{
			{
				"message": map[string]string{
					"role":    "assistant",
					"content": assembled,
				},
			},
		},
	})
	if err != nil {
		slog.Error("failed to marshal synthetic stream response", "error", err)
		return "", streamModel, streamDone
	}
	return string(synth), streamModel, streamDone
}

func hasMessages(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return false
	}
	return len(arr) > 0
}

func httpError(w http.ResponseWriter, code int, errType string, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	msg := fmt.Sprintf(format, args...)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    errType,
		},
	})
}
