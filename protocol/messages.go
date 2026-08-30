// Package protocol defines the wire contract between the coordinator and
// provider nodes. Messages are JSON envelopes sent over the provider's
// outbound WebSocket connection:
//
//	{ "type": "<message type>", "data": { ... } }
//
// This mirrors Darkbloom's coordinator/protocol <-> provider protocol split.
// v0 sends plaintext inference payloads; E2E NaCl Box sealing is the v1 layer
// and only adds an `encrypted_body` field to InferenceRequest/Chunk.
package protocol

import (
	"encoding/json"
	"fmt"
)

// Message types on the wire.
const (
	TypeRegister          = "register"
	TypeRegisterOK        = "register_ok"
	TypeRegisterDenied    = "register_denied"
	TypeHeartbeat         = "heartbeat"
	TypeInferenceRequest  = "inference_request"
	TypeInferenceAccepted = "inference_accepted"
	TypeInferenceChunk    = "inference_chunk"
	TypeInferenceComplete = "inference_complete"
	TypeInferenceError    = "inference_error"
	TypeCancel            = "cancel"
)

// Envelope is the outer frame for every WebSocket message.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// New builds an Envelope of the given type, marshaling payload to JSON.
func New(t string, payload any) (Envelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal %s: %w", t, err)
	}
	return Envelope{Type: t, Data: data}, nil
}

// Decode unmarshals the envelope's data payload into v.
func (e Envelope) Decode(v any) error {
	return json.Unmarshal(e.Data, v)
}

// Register is the first message a provider sends after connecting.
type Register struct {
	NodeID         string   `json:"node_id"` // optional; assigned by coordinator if empty
	Name           string   `json:"name"`
	Chip           string   `json:"chip"`
	MemoryGB       int      `json:"memory_gb"`
	Models         []string `json:"models"` // models resident and servable right now
	Version        string   `json:"version"`
	JoinCode       string   `json:"join_code,omitempty"`       // required when coordinator sets one
	EnrollmentCode string   `json:"enrollment_code,omitempty"` // binds node to an account
}

// RegisterDenied rejects a registration attempt (e.g. bad join code).
// The coordinator closes the connection after sending this.
type RegisterDenied struct {
	Reason string `json:"reason"`
}

// RegisterOK acknowledges registration and sets the heartbeat cadence.
type RegisterOK struct {
	HeartbeatIntervalSecs int `json:"heartbeat_interval_secs"`
}

// Heartbeat keeps a node marked online and reports live capacity.
type Heartbeat struct {
	NodeID       string  `json:"node_id"`
	FreeMemoryGB float64 `json:"free_memory_gb"`
	QueueDepth   int     `json:"queue_depth"`
}

// InferenceRequest carries one OpenAI-shaped chat completion request.
// Body is the raw OpenAI request JSON (v0: plaintext; v1: sealed).
type InferenceRequest struct {
	RequestID string          `json:"request_id"`
	Model     string          `json:"model"`
	Stream    bool            `json:"stream"`
	Body      json.RawMessage `json:"body"`
}

// InferenceAccepted tells the coordinator the node picked the job up.
type InferenceAccepted struct {
	RequestID string `json:"request_id"`
}

// InferenceChunk streams one delta of generated text.
type InferenceChunk struct {
	RequestID string `json:"request_id"`
	Delta     string `json:"delta"`
}

// Usage reports token counts for metering/billing.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// InferenceComplete terminates a successful request.
type InferenceComplete struct {
	RequestID string `json:"request_id"`
	Usage     Usage  `json:"usage"`
}

// InferenceError terminates a failed request.
type InferenceError struct {
	RequestID string `json:"request_id"`
	Error     string `json:"error"`
}

// Cancel asks the provider to stop generating for a request.
type Cancel struct {
	RequestID string `json:"request_id"`
}
