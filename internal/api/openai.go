package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/kalambet/tbyd/internal/pipeline"
	"github.com/kalambet/tbyd/internal/proxy"
)

const maxRequestBodySize = 1 << 20 // 1MB

// NewOpenAIHandler returns an http.Handler implementing the OpenAI-compatible
// REST API. When enricher is non-nil, incoming chat requests are enriched
// before forwarding to the cloud proxy. Passing nil disables enrichment
// (passthrough mode).
func NewOpenAIHandler(p *proxy.Client, enricher *pipeline.Enricher) http.Handler {
	r := chi.NewRouter()

	r.Get("/health", handleHealth)
	r.Get("/v1/models", handleModels(p))
	r.Post("/v1/chat/completions", handleChatCompletions(p, enricher))

	return r
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
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

func handleChatCompletions(p *proxy.Client, enricher *pipeline.Enricher) http.HandlerFunc {
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

		rc, err := p.Chat(r.Context(), req)
		if err != nil {
			httpError(w, http.StatusBadGateway, "api_error", "upstream error: %v", err)
			return
		}
		defer rc.Close()

		if req.Stream {
			streamResponse(w, rc)
		} else {
			body, err := io.ReadAll(rc)
			if err != nil {
				httpError(w, http.StatusBadGateway, "api_error", "reading upstream response: %v", err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		}
	}
}

func streamResponse(w http.ResponseWriter, rc io.Reader) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "api_error", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	reader := bufio.NewReader(rc)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			w.Write(line)
			flusher.Flush()
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("upstream stream read error: %v", err)
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
					log.Printf("failed to marshal stream error payload: %v", marshalErr)
				}
			}
			break
		}
	}
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
