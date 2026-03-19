package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kalambet/tbyd/internal/proxy"
)

// mockUpstream returns an httptest.Server that mimics a subset of the OpenRouter API.
func mockUpstream(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *proxy.Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := proxy.NewClientWithBaseURL("test-key", srv.URL)
	return srv, c
}

func TestHealth(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {})
	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var body map[string]any
	json.NewDecoder(rr.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("body = %v, want status=ok", body)
	}
	if dropped, ok := body["dropped_interactions"].(float64); !ok || dropped != 0 {
		t.Errorf("dropped_interactions = %v, want 0", body["dropped_interactions"])
	}
}

func TestChatCompletions_Streaming(t *testing.T) {
	sseData := "data: {\"id\":\"gen-1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\ndata: [DONE]\n\n"

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseData)
	})
	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}

	got := rr.Body.String()
	if !strings.Contains(got, `"choices"`) {
		t.Errorf("body does not contain expected SSE data: %q", got)
	}
}

func TestChatCompletions_NonStreaming(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})
	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":false}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	if rr.Body.String() != respJSON {
		t.Errorf("body = %q, want %q", rr.Body.String(), respJSON)
	}
}

func TestChatCompletions_InvalidBody(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {})
	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{invalid"))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestChatCompletions_MissingMessages(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {})
	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[]}`))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestChatCompletions_UpstreamError(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"internal failure","type":"server_error"}}`)
	})
	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}

	var errResp map[string]map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["error"]["type"] != "api_error" {
		t.Errorf("error.type = %q, want %q", errResp["error"]["type"], "api_error")
	}
	if !strings.Contains(errResp["error"]["message"], "upstream error") {
		t.Errorf("error.message = %q, want it to contain 'upstream error'", errResp["error"]["message"])
	}
}

func TestChatCompletions_StreamingMidStreamError(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send one valid chunk then abruptly close the connection.
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"gen-1\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n")
		flusher.Flush()

		// Hijack and close the raw TCP connection to cause a read error.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("upstream does not support hijacking")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	})
	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	got := rr.Body.String()
	if !strings.Contains(got, `"Hi"`) {
		t.Errorf("response missing first chunk: %q", got)
	}
	if !strings.Contains(got, `"server_error"`) {
		t.Errorf("response missing SSE error event: %q", got)
	}
}

func TestBindsToLoopback(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {})
	handler, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	srv := &http.Server{
		Addr:    "127.0.0.1:0",
		Handler: handler,
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	if !strings.HasPrefix(addr, "127.0.0.1") {
		t.Errorf("listener address = %q, want prefix 127.0.0.1", addr)
	}
}

func TestModels(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		list := proxy.ModelList{
			Object: "list",
			Data: []proxy.Model{
				{ID: "anthropic/claude-opus-4", Object: "model"},
				{ID: "openai/gpt-4o", Object: "model"},
			},
		}
		json.NewEncoder(w).Encode(list)
	})
	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var list proxy.ModelList
	json.NewDecoder(rr.Body).Decode(&list)

	if len(list.Data) != 2 {
		t.Fatalf("got %d models, want 2", len(list.Data))
	}
	if list.Data[0].ID != "anthropic/claude-opus-4" {
		t.Errorf("models[0].ID = %q", list.Data[0].ID)
	}
}

// TestChatCompletions_NonStreaming_SurfacesInteractionID verifies that when
// save_interactions is enabled a non-streaming response includes the
// X-TBYD-Interaction-ID header containing a valid UUID.
func TestChatCompletions_NonStreaming_SurfacesInteractionID(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	saver := &mockInteractionSaver{saveDone: make(chan struct{}, 1)}
	h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	id := rr.Header().Get("X-TBYD-Interaction-ID")
	if id == "" {
		t.Fatal("X-TBYD-Interaction-ID header is absent, want non-empty UUID")
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Errorf("X-TBYD-Interaction-ID = %q is not a valid UUID: %v", id, err)
	}
}

// TestChatCompletions_NonStreaming_NoIDWhenSaveDisabled verifies that when
// save_interactions is disabled the X-TBYD-Interaction-ID header is absent.
func TestChatCompletions_NonStreaming_NoIDWhenSaveDisabled(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	if id := rr.Header().Get("X-TBYD-Interaction-ID"); id != "" {
		t.Errorf("X-TBYD-Interaction-ID = %q, want absent when save disabled", id)
	}
}

// TestChatCompletions_Streaming_SurfacesInteractionID verifies that a streaming
// response contains a tbyd-metadata SSE event with a valid interaction_id field
// when save_interactions is enabled.
func TestChatCompletions_Streaming_SurfacesInteractionID(t *testing.T) {
	sseData := "data: {\"id\":\"gen-1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: [DONE]\n\n"

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseData)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	saver := &mockInteractionSaver{saveDone: make(chan struct{}, 1)}
	h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	rawBody := rr.Body.String()
	if !strings.Contains(rawBody, "event: tbyd-metadata") {
		t.Fatalf("SSE stream missing tbyd-metadata event:\n%s", rawBody)
	}

	// Extract the data line after "event: tbyd-metadata".
	var interactionID string
	lines := strings.Split(rawBody, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "event: tbyd-metadata" && i+1 < len(lines) {
			dataLine := strings.TrimSpace(lines[i+1])
			if strings.HasPrefix(dataLine, "data: ") {
				payload := strings.TrimPrefix(dataLine, "data: ")
				var meta map[string]string
				if err := json.Unmarshal([]byte(payload), &meta); err != nil {
					t.Fatalf("tbyd-metadata data is not valid JSON: %v — raw: %q", err, payload)
				}
				interactionID = meta["interaction_id"]
			}
		}
	}

	if interactionID == "" {
		t.Fatal("tbyd-metadata event found but interaction_id field is absent or empty")
	}
	if _, err := uuid.Parse(interactionID); err != nil {
		t.Errorf("interaction_id = %q is not a valid UUID: %v", interactionID, err)
	}
}

// TestChatCompletions_Streaming_NoMetadataWhenSaveDisabled verifies that when
// save_interactions is disabled no tbyd-metadata event is emitted.
func TestChatCompletions_Streaming_NoMetadataWhenSaveDisabled(t *testing.T) {
	sseData := "data: {\"id\":\"gen-1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: [DONE]\n\n"

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseData)
	})

	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, false, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	if strings.Contains(rr.Body.String(), "tbyd-metadata") {
		t.Errorf("SSE stream contains tbyd-metadata event but save is disabled:\n%s", rr.Body.String())
	}
}

// TestChatCompletions_InteractionID_MatchesSavedRecord verifies that the
// interaction ID surfaced in the response header is the same ID used when
// persisting the interaction to the store.
func TestChatCompletions_InteractionID_MatchesSavedRecord(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	saver := &mockInteractionSaver{saveDone: make(chan struct{}, 1)}
	h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"match me"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	surfacedID := rr.Header().Get("X-TBYD-Interaction-ID")
	if surfacedID == "" {
		t.Fatal("X-TBYD-Interaction-ID header is absent")
	}

	// Wait for the async save to complete.
	select {
	case <-saver.saveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for interaction save")
	}

	interactions := saver.getInteractions()
	if len(interactions) != 1 {
		t.Fatalf("saved %d interactions, want 1", len(interactions))
	}

	savedID := interactions[0].ID
	if savedID != surfacedID {
		t.Errorf("surfaced ID %q does not match saved interaction ID %q", surfacedID, savedID)
	}

	if interactions[0].UserQuery != "match me" {
		t.Errorf("UserQuery = %q, want %q", interactions[0].UserQuery, "match me")
	}
}

// TestChatCompletions_Streaming_MetadataEventFormat verifies the exact SSE wire
// format of the tbyd-metadata event: event field, data field, and double newline
// terminator must appear in sequence.
func TestChatCompletions_Streaming_MetadataEventFormat(t *testing.T) {
	sseData := "data: {\"id\":\"gen-1\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n" +
		"data: [DONE]\n\n"

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseData)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	saver := &mockInteractionSaver{saveDone: make(chan struct{}, 1)}
	h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, false, nil)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	rawBody := rr.Body.String()

	// The exact sequence must be:  "event: tbyd-metadata\ndata: {...}\n\n"
	// followed eventually by the [DONE] line.
	const eventPrefix = "event: tbyd-metadata\n"
	idx := strings.Index(rawBody, eventPrefix)
	if idx == -1 {
		t.Fatalf("tbyd-metadata event not found in SSE body:\n%s", rawBody)
	}

	after := rawBody[idx+len(eventPrefix):]
	if !strings.HasPrefix(after, "data: ") {
		t.Errorf("expected 'data: ' immediately after event line, got: %q", after[:min(40, len(after))])
	}

	// Verify the event appears before [DONE].
	doneIdx := strings.Index(rawBody, "data: [DONE]")
	if doneIdx == -1 {
		t.Fatal("[DONE] not found in SSE body")
	}
	if idx > doneIdx {
		t.Errorf("tbyd-metadata event appears after [DONE], want before")
	}

	// Verify the data line contains a JSON object (not bare data:).
	dataLineEnd := strings.Index(after, "\n")
	if dataLineEnd == -1 {
		t.Fatal("no newline after data line")
	}
	dataLine := after[:dataLineEnd]
	jsonPart := strings.TrimPrefix(dataLine, "data: ")
	var meta map[string]string
	if err := json.Unmarshal([]byte(jsonPart), &meta); err != nil {
		t.Errorf("data field is not valid JSON: %v — raw: %q", err, jsonPart)
	}
	if meta["interaction_id"] == "" {
		t.Errorf("interaction_id field missing in metadata JSON: %q", jsonPart)
	}

	// Verify the event block ends with \n\n (double newline — SSE event terminator).
	eventBlock := rawBody[idx : strings.Index(rawBody[idx:], "\n\n")+idx+2]
	if !strings.HasSuffix(eventBlock, "\n\n") {
		t.Errorf("SSE event block does not end with double newline: %q", eventBlock)
	}
}

// TestChatCompletions_InteractionID_IsUUID verifies that the surfaced interaction
// ID passes uuid.Parse without error for both streaming and non-streaming paths.
func TestChatCompletions_InteractionID_IsUUID(t *testing.T) {
	t.Run("non-streaming", func(t *testing.T) {
		respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hi"}}]}`
		_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, respJSON)
		})

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		saver := &mockInteractionSaver{saveDone: make(chan struct{}, 1)}
		h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, false, nil)

		body := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		h.ServeHTTP(rr, req)

		id := rr.Header().Get("X-TBYD-Interaction-ID")
		if id == "" {
			t.Fatal("X-TBYD-Interaction-ID header absent")
		}
		if _, err := uuid.Parse(id); err != nil {
			t.Errorf("header value %q is not a valid UUID: %v", id, err)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		sseData := "data: {\"id\":\"gen-1\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n" +
			"data: [DONE]\n\n"
		_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, sseData)
		})

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		saver := &mockInteractionSaver{saveDone: make(chan struct{}, 1)}
		h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, false, nil)

		body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		h.ServeHTTP(rr, req)

		rawBody := rr.Body.String()
		lines := strings.Split(rawBody, "\n")
		var id string
		for i, line := range lines {
			if strings.TrimSpace(line) == "event: tbyd-metadata" && i+1 < len(lines) {
				dataLine := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(dataLine, "data: ") {
					var meta map[string]string
					payload := strings.TrimPrefix(dataLine, "data: ")
					if err := json.Unmarshal([]byte(payload), &meta); err == nil {
						id = meta["interaction_id"]
					}
				}
			}
		}

		if id == "" {
			t.Fatal("interaction_id not found in tbyd-metadata event")
		}
		if _, err := uuid.Parse(id); err != nil {
			t.Errorf("interaction_id %q is not a valid UUID: %v", id, err)
		}
	})
}


