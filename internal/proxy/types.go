package proxy

import "encoding/json"

// ChatRequest is the OpenAI-compatible chat completion request.
// Fields not explicitly modeled are preserved in Extra for pass-through.
type ChatRequest struct {
	Model    string                     `json:"model"`
	Messages json.RawMessage            `json:"messages"`
	Stream   bool                       `json:"stream,omitempty"`
	Extra    map[string]json.RawMessage `json:"-"`
}

func (r ChatRequest) MarshalJSON() ([]byte, error) {
	m := make(map[string]json.RawMessage)
	for k, v := range r.Extra {
		m[k] = v
	}
	if r.Model != "" {
		b, _ := json.Marshal(r.Model)
		m["model"] = b
	}
	if r.Messages != nil {
		m["messages"] = r.Messages
	}
	if r.Stream {
		m["stream"] = json.RawMessage(`true`)
	}
	return json.Marshal(m)
}

func (r *ChatRequest) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["model"]; ok {
		json.Unmarshal(v, &r.Model)
		delete(raw, "model")
	}
	if v, ok := raw["messages"]; ok {
		r.Messages = v
		delete(raw, "messages")
	}
	if v, ok := raw["stream"]; ok {
		json.Unmarshal(v, &r.Stream)
		delete(raw, "stream")
	}
	r.Extra = raw
	return nil
}

// Model represents a model entry returned by the /v1/models endpoint.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// ModelList is the response from /v1/models.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}
