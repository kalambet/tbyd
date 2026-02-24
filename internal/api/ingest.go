package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/storage"
)

const maxIngestBodySize = 10 << 20 // 10MB
const maxURLFetchSize = 5 << 20    // 5MB

type IngestRequest struct {
	Source   string            `json:"source"`
	Type     string            `json:"type"`
	Title    string            `json:"title"`
	Content  string            `json:"content"`
	URL      string            `json:"url"`
	Tags     []string          `json:"tags"`
	Metadata map[string]string `json:"metadata"`
}

// VectorDeleter abstracts vector store deletion for the API layer.
type VectorDeleter interface {
	Delete(table string, id string) error
}

type AppDeps struct {
	Store      *storage.Store
	Profile    *profile.Manager
	Token      string
	HTTPClient *http.Client
	Vectors    VectorDeleter // optional; if nil, vector cleanup is skipped on delete
}

func NewAppHandler(deps AppDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(BearerAuth(deps.Token))

	r.Post("/ingest", handleIngest(deps))
	r.Get("/profile", handleGetProfile(deps))
	r.Patch("/profile", handlePatchProfile(deps))
	r.Get("/interactions", handleListInteractions(deps))
	r.Get("/interactions/{id}", handleGetInteraction(deps))
	r.Delete("/interactions/{id}", handleDeleteInteraction(deps))
	r.Get("/context-docs", handleListContextDocs(deps))
	r.Delete("/context-docs/{id}", handleDeleteContextDoc(deps))

	return r
}

func handleIngest(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxIngestBodySize)
		defer r.Body.Close()

		var req IngestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body: %v", err)
			return
		}

		if req.Source == "" {
			httpError(w, http.StatusBadRequest, "invalid_request_error", "source is required")
			return
		}
		if req.Content == "" && req.URL == "" {
			httpError(w, http.StatusBadRequest, "invalid_request_error", "at least one of content or url is required")
			return
		}
		if req.Type == "" {
			req.Type = "text"
		}

		var resolvedContent string
		switch {
		case req.Type == "url" && req.URL != "":
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()

			httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
			if err != nil {
				httpError(w, http.StatusBadRequest, "invalid_request_error", "invalid url: %v", err)
				return
			}
			resp, err := deps.HTTPClient.Do(httpReq)
			if err != nil {
				httpError(w, http.StatusBadGateway, "api_error", "failed to fetch url: %v", err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				httpError(w, http.StatusBadGateway, "api_error", "url returned status %d", resp.StatusCode)
				return
			}

			body, err := io.ReadAll(io.LimitReader(resp.Body, maxURLFetchSize))
			if err != nil {
				httpError(w, http.StatusBadGateway, "api_error", "failed to read url response: %v", err)
				return
			}
			resolvedContent = string(body)
			if req.Title == "" {
				req.Title = req.URL
			}

		case req.Type == "file" && req.Content != "":
			decoded, err := base64.StdEncoding.DecodeString(req.Content)
			if err != nil {
				httpError(w, http.StatusBadRequest, "invalid_request_error", "invalid base64 content")
				return
			}
			resolvedContent = string(decoded)

		default:
			resolvedContent = req.Content
		}

		docID := uuid.New().String()

		tagsJSON := "[]"
		if req.Tags != nil {
			b, err := json.Marshal(req.Tags)
			if err != nil {
				httpError(w, http.StatusInternalServerError, "api_error", "failed to marshal tags: %v", err)
				return
			}
			tagsJSON = string(b)
		}

		doc := storage.ContextDoc{
			ID:        docID,
			Title:     req.Title,
			Content:   resolvedContent,
			Source:    req.Source,
			Tags:      tagsJSON,
			CreatedAt: time.Now().UTC(),
		}
		if err := deps.Store.SaveContextDoc(doc); err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to save document: %v", err)
			return
		}

		payload, err := json.Marshal(map[string]string{"context_doc_id": docID})
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to create job payload: %v", err)
			return
		}
		job := storage.Job{
			ID:          uuid.New().String(),
			Type:        "ingest_enrich",
			PayloadJSON: string(payload),
		}
		if err := deps.Store.EnqueueJob(job); err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to enqueue job: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"id":     docID,
			"status": "queued",
		})
	}
}

func handleGetProfile(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := deps.Profile.GetProfile()
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to get profile: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(p)
	}
}

func handlePatchProfile(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
		var fields map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			httpError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body: %v", err)
			return
		}

		for key, value := range fields {
			if err := deps.Profile.SetField(key, value); err != nil {
				httpError(w, http.StatusInternalServerError, "api_error", "failed to set field %q: %v", key, err)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	}
}

func handleListInteractions(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := parseIntParam(r, "limit", 20, 100)
		offset := parseIntParam(r, "offset", 0, 0)

		interactions, err := deps.Store.ListInteractions(limit, offset)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to list interactions: %v", err)
			return
		}

		if interactions == nil {
			interactions = []storage.Interaction{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(interactions)
	}
}

func handleGetInteraction(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		interaction, err := deps.Store.GetInteraction(id)
		if errors.Is(err, storage.ErrNotFound) {
			httpError(w, http.StatusNotFound, "not_found", "interaction not found")
			return
		}
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to get interaction: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(interaction)
	}
}

func handleDeleteInteraction(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		// Clean up vectors if VectorStore is available.
		if deps.Vectors != nil {
			interaction, err := deps.Store.GetInteraction(id)
			if errors.Is(err, storage.ErrNotFound) {
				httpError(w, http.StatusNotFound, "not_found", "interaction not found")
				return
			}
			if err != nil {
				httpError(w, http.StatusInternalServerError, "api_error", "failed to get interaction: %v", err)
				return
			}
			deleteVectorIDs(deps.Vectors, interaction.VectorIDs)
		}

		err := deps.Store.DeleteInteraction(id)
		if errors.Is(err, storage.ErrNotFound) {
			httpError(w, http.StatusNotFound, "not_found", "interaction not found")
			return
		}
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to delete interaction: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	}
}

// deleteVectorIDs parses a JSON array of vector IDs and deletes each from the vector store.
func deleteVectorIDs(vectors VectorDeleter, vectorIDsJSON string) {
	var ids []string
	if err := json.Unmarshal([]byte(vectorIDsJSON), &ids); err != nil {
		return
	}
	for _, vid := range ids {
		_ = vectors.Delete("context_vectors", vid)
	}
}

func handleListContextDocs(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := parseIntParam(r, "limit", 20, 100)
		offset := parseIntParam(r, "offset", 0, 0)

		docs, err := deps.Store.ListContextDocsPaginated(limit, offset)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to list context docs: %v", err)
			return
		}

		if docs == nil {
			docs = []storage.ContextDoc{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(docs)
	}
}

func handleDeleteContextDoc(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		// Fetch doc to get vector_id before deleting.
		if deps.Vectors != nil {
			doc, err := deps.Store.GetContextDoc(id)
			if errors.Is(err, storage.ErrNotFound) {
				httpError(w, http.StatusNotFound, "not_found", "context doc not found")
				return
			}
			if err != nil {
				httpError(w, http.StatusInternalServerError, "api_error", "failed to get context doc: %v", err)
				return
			}
			if doc.VectorID != "" {
				_ = deps.Vectors.Delete("context_vectors", doc.VectorID)
			}
		}

		err := deps.Store.DeleteContextDoc(id)
		if errors.Is(err, storage.ErrNotFound) {
			httpError(w, http.StatusNotFound, "not_found", "context doc not found")
			return
		}
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to delete context doc: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	}
}

func parseIntParam(r *http.Request, key string, defaultVal, maxVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return defaultVal
	}
	if maxVal > 0 && v > maxVal {
		return maxVal
	}
	return v
}
