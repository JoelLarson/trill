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
