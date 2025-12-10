package codex

import (
	"testing"
)

func TestParseCodexJSON(t *testing.T) {
	logs := []byte(`{"type":"thread.started","thread_id":"abc"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"hello"}}`)

	session, reply, err := parseCodexJSON(logs)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if session != "abc" {
		t.Fatalf("expected session abc, got %s", session)
	}
	if reply != "hello" {
		t.Fatalf("expected reply hello, got %s", reply)
	}
}
