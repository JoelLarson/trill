package service

import (
	"context"
	"errors"
	"testing"

	"trill/internal/store"
)

type fakeModel struct {
	reply      string
	sessionID  string
	durationMS int64
	err        error
}

func (f *fakeModel) Send(ctx context.Context, sessionID, prompt string) (string, string, string, int64, error) {
	if f.err != nil {
		return "", "", sessionID, f.durationMS, f.err
	}
	if f.sessionID == "" {
		f.sessionID = "sess-1"
	}
	return f.reply, "raw", f.sessionID, f.durationMS, nil
}

type scriptedModel struct {
	replies   []string
	sessionID string
	idx       int
}

func (m *scriptedModel) Send(ctx context.Context, sessionID, prompt string) (string, string, string, int64, error) {
	if m.sessionID == "" {
		m.sessionID = "sess-scripted"
	}
	if m.idx >= len(m.replies) {
		return "", "", m.sessionID, 0, errors.New("no more replies")
	}
	reply := m.replies[m.idx]
	m.idx++
	return reply, "raw", m.sessionID, 10, nil
}

func TestSendCreatesAndPersistsConversation(t *testing.T) {
	st := store.NewMemoryStore()
	model := &fakeModel{reply: "world", durationMS: 100}
	svc := New(st, model, nil)

	call, err := svc.Send(context.Background(), "", "hello")
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if call.SessionID == "" {
		t.Fatalf("session id missing")
	}
	conv, err := st.Get(context.Background(), call.SessionID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if len(conv.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(conv.Messages))
	}
	if conv.Messages[0].Content != "hello" || conv.Messages[1].Content != "world" {
		t.Fatalf("unexpected messages: %+v", conv.Messages)
	}
	if len(conv.ModelCalls) != 1 {
		t.Fatalf("expected 1 model call, got %d", len(conv.ModelCalls))
	}
}

func TestSendReturnsErrorOnModelFailure(t *testing.T) {
	st := store.NewMemoryStore()
	model := &fakeModel{err: errors.New("boom")}
	svc := New(st, model, nil)
	if _, err := svc.Send(context.Background(), "", "hi"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestAutoProvideInfoAdvancesExecution(t *testing.T) {
	st := store.NewMemoryStore()
	model := &scriptedModel{
		replies: []string{
			"1) detect system",
			"NEED: Which OS and package managers?",
			"COMMAND: echo detecting",
			"SUCCESS: collected",
		},
	}
	svc := New(st, model, nil)
	conv, err := svc.CreateConversation(context.Background(), "Check environment")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	conv, err = svc.ApprovePlan(context.Background(), conv.SessionID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if conv.State != "awaiting_command" {
		t.Fatalf("expected awaiting_command after NEED, got %s", conv.State)
	}
	if conv.Steps[0].PendingCommand != "echo detecting" {
		t.Fatalf("pending command not captured: %+v", conv.Steps[0])
	}
}
