package types

import "time"

// Message represents a single chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ModelCall captures one Codex invocation.
type ModelCall struct {
	Prompt     string    `json:"prompt"`
	RawOutput  string    `json:"raw_output"`
	Reply      string    `json:"reply"`
	Timestamp  time.Time `json:"timestamp"`
	DurationMS int64     `json:"duration_ms"`
	SessionID  string    `json:"session_id"`
}

// Conversation stores the persisted chat context for a Codex session.
type Conversation struct {
	SessionID  string      `json:"session_id"`
	Messages   []Message   `json:"messages"`
	ModelCalls []ModelCall `json:"model_calls"`
}
