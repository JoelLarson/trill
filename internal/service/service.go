package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent-manager/internal/codex"
	"agent-manager/internal/store"
	"agent-manager/internal/types"
)

type Service struct {
	store store.ConversationStore
	model codex.Client
	clock func() time.Time
}

func New(store store.ConversationStore, model codex.Client) *Service {
	return &Service{
		store: store,
		model: model,
		clock: time.Now,
	}
}

// Start returns an empty id for compatibility; real conversations are created on first send.
func (s *Service) Start(ctx context.Context) (string, error) {
	return "", nil
}

func (s *Service) Send(ctx context.Context, sessionID, msg string) (*types.ModelCall, error) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil, fmt.Errorf("message is required")
	}
	var conv *types.Conversation
	if sessionID != "" {
		found, err := s.store.Get(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		conv = found
	} else {
		conv = &types.Conversation{}
	}
	conv.Messages = append(conv.Messages, types.Message{Role: "user", Content: msg})
	reply, raw, newSessionID, duration, err := s.model.Send(ctx, conv.SessionID, msg)
	if err != nil {
		return nil, err
	}
	call := types.ModelCall{
		Prompt:     msg,
		RawOutput:  raw,
		Reply:      reply,
		Timestamp:  s.clock(),
		DurationMS: duration,
		SessionID:  newSessionID,
	}
	conv.SessionID = newSessionID
	conv.Messages = append(conv.Messages, types.Message{Role: "assistant", Content: reply})
	conv.ModelCalls = append(conv.ModelCalls, call)
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	return &call, nil
}

func (s *Service) List(ctx context.Context) ([]string, error) {
	return s.store.ListIDs(ctx)
}

func (s *Service) Get(ctx context.Context, sessionID string) (*types.Conversation, error) {
	return s.store.Get(ctx, sessionID)
}

func (s *Service) Close(ctx context.Context, sessionID string) error {
	return s.store.Delete(ctx, sessionID)
}

// PlanAndExecute preserves original behavior: get a plan then iterate steps using the model.
func (s *Service) PlanAndExecute(ctx context.Context, goal string) (string, error) {
	if strings.TrimSpace(goal) == "" {
		return "", fmt.Errorf("goal is required")
	}
	sessionID := ""
	planPrompt := "Provide a high‑level plan (as a numbered list) to achieve the following goal: " + goal
	plan, _, newSession, _, err := s.model.Send(ctx, sessionID, planPrompt)
	if err != nil {
		return "", err
	}
	sessionID = newSession
	contextStr := "Goal: " + goal + "\nPlan:\n" + plan
	for {
		stepPrompt := contextStr + "\n\nProvide the next actionable step or reply with DONE when the goal is solved. Use the format COMMAND: <cmd> <args> for actions."
		step, _, newSession, _, err := s.model.Send(ctx, sessionID, stepPrompt)
		if err != nil {
			return "", err
		}
		sessionID = newSession
		trimmed := strings.TrimSpace(step)
		upper := strings.ToUpper(trimmed)
		if upper == "DONE" || upper == "FINISHED" || strings.Contains(trimmed, "\"status\":\"complete\"") {
			return "Goal solved.\n\n" + contextStr, nil
		}
		contextStr += "\nModel output: " + trimmed
	}
}
