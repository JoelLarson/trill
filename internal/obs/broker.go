package obs

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Event captures observability data for Codex interactions and execution flow.
type Event struct {
	Timestamp   time.Time `json:"timestamp"`
	Type        string    `json:"type"`
	SessionID   string    `json:"session_id"`
	Prompt      string    `json:"prompt,omitempty"`
	ModelPrompt string    `json:"model_prompt,omitempty"`
	PlanText    string    `json:"plan_text,omitempty"`
	StepID      string    `json:"step_id,omitempty"`
	StepTitle   string    `json:"step_title,omitempty"`
	Command     string    `json:"command,omitempty"`
	RawOutput   string    `json:"raw_output,omitempty"`
	Reply       string    `json:"reply,omitempty"`
	Note        string    `json:"note,omitempty"`
	ArtifactID  string    `json:"artifact_id,omitempty"`
}

type Broker struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[chan Event]struct{})}
}

func (b *Broker) Publish(ev Event) {
	ev.Timestamp = time.Now()
	b.mu.RLock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	b.mu.RUnlock()
}

func (b *Broker) Subscribe() chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *Broker) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// SSEHandler streams events as newline-delimited JSON with SSE framing.
func (b *Broker) SSEHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			w.Write([]byte("data: "))
			_ = enc.Encode(ev)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}
