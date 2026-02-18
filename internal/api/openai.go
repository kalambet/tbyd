package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/kalambet/tbyd/internal/proxy"
)

// NewOpenAIHandler returns an http.Handler implementing the OpenAI-compatible
// REST API in passthrough mode.
func NewOpenAIHandler(p *proxy.Client) http.Handler {
	r := chi.NewRouter()

	r.Get("/health", handleHealth)
	r.Get("/v1/models", handleModels(p))
	r.Post("/v1/chat/completions", handleChatCompletions(p))

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
			httpError(w, http.StatusBadGateway, "failed to list models: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(proxy.ModelList{
			Object: "list",
			Data:   models,
		})
	}
}

func handleChatCompletions(p *proxy.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var req proxy.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, "invalid request body: %v", err)
			return
		}

		if !hasMessages(req.Messages) {
			httpError(w, http.StatusBadRequest, "messages is required and must not be empty")
			return
		}

		rc, err := p.Chat(r.Context(), req)
		if err != nil {
			httpError(w, http.StatusBadGateway, "upstream error: %v", err)
			return
		}
		defer rc.Close()

		if req.Stream {
			streamResponse(w, rc)
		} else {
			w.Header().Set("Content-Type", "application/json")
			io.Copy(w, rc)
		}
	}
}

func streamResponse(w http.ResponseWriter, rc io.Reader) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	scanner := bufio.NewScanner(rc)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintf(w, "%s\n", line)
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(w, "data: {\"error\":{\"message\":\"upstream read error\",\"type\":\"server_error\"}}\n\n")
		flusher.Flush()
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

func httpError(w http.ResponseWriter, code int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	msg := fmt.Sprintf(format, args...)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "invalid_request_error",
			"code":    code,
		},
	})
}
