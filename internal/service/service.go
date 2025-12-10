package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"trill/internal/codex"
	"trill/internal/store"
	"trill/internal/types"
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

// Start returns an empty id for compatibility with legacy clients.
func (s *Service) Start(ctx context.Context) (string, error) {
	return "", nil
}

// CreateConversation seeds a plan and moves to awaiting plan approval.
func (s *Service) CreateConversation(ctx context.Context, goal string) (*types.Conversation, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil, fmt.Errorf("goal is required")
	}
	planPrompt := seedPrompt(goal)
	reply, raw, sessionID, duration, err := s.model.Send(ctx, "", planPrompt)
	if err != nil {
		return nil, err
	}
	steps := parsePlan(reply)
	conv := &types.Conversation{
		SessionID:      sessionID,
		Goal:           goal,
		State:          types.StateAwaitingPlanApproval,
		PlanVersion:    1,
		PlanText:       reply,
		AwaitingReason: "Awaiting plan approval",
		Steps:          steps,
		ModelCalls: []types.ModelCall{{
			Prompt:     planPrompt,
			RawOutput:  raw,
			Reply:      reply,
			Timestamp:  s.clock(),
			DurationMS: duration,
			SessionID:  sessionID,
		}},
	}
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

func (s *Service) ApprovePlan(ctx context.Context, sessionID string) (*types.Conversation, error) {
	conv, err := s.store.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if conv.State != types.StateAwaitingPlanApproval {
		return nil, fmt.Errorf("conversation not awaiting plan approval")
	}
	conv.State = types.StateExecuting
	conv.AwaitingReason = ""
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	return s.advanceExecution(ctx, conv)
}

func (s *Service) Resume(ctx context.Context, sessionID string) (*types.Conversation, error) {
	conv, err := s.store.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if conv.State != types.StateBlocked {
		return conv, nil
	}
	conv.State = types.StateExecuting
	conv.AwaitingReason = ""
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	return s.advanceExecution(ctx, conv)
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

// ApproveCommand executes a pending command for a blocked step.
func (s *Service) ApproveCommand(ctx context.Context, sessionID, stepID string) (*types.Conversation, error) {
	conv, err := s.store.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var target *types.Step
	for i := range conv.Steps {
		if conv.Steps[i].ID == stepID {
			target = &conv.Steps[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("step %s not found", stepID)
	}
	if target.PendingCommand == "" {
		return nil, fmt.Errorf("no pending command for step %s", stepID)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", target.PendingCommand)
	out, err := cmd.CombinedOutput()
	output := string(out)
	target.Logs = append(target.Logs, "EXEC: "+target.PendingCommand, output)
	target.PendingCommand = ""
	if err != nil {
		target.Status = types.StepBlocked
		conv.State = types.StateBlocked
		conv.AwaitingReason = fmt.Sprintf("Command failed: %v", err)
		_ = s.store.Save(ctx, conv)
		return conv, nil
	}
	target.Status = types.StepDone
	target.CompletedAt = s.clock()
	conv.State = types.StateExecuting
	conv.AwaitingReason = ""
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	return s.advanceExecution(ctx, conv)
}

func (s *Service) PlanAndExecute(ctx context.Context, goal string) (string, error) {
	conv, err := s.CreateConversation(ctx, goal)
	if err != nil {
		return "", err
	}
	conv.State = types.StateExecuting
	conv.AwaitingReason = ""
	if err := s.store.Save(ctx, conv); err != nil {
		return "", err
	}
	conv, err = s.advanceExecution(ctx, conv)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Conversation %s completed with state %s", conv.SessionID, conv.State), nil
}

func (s *Service) ListInbox(ctx context.Context) ([]types.InboxItem, error) {
	ids, err := s.store.ListIDs(ctx)
	if err != nil {
		return nil, err
	}
	var inbox []types.InboxItem
	for _, id := range ids {
		conv, err := s.store.Get(ctx, id)
		if err != nil {
			continue
		}
		if conv.State == types.StateAwaitingPlanApproval || conv.State == types.StateBlocked {
			inbox = append(inbox, types.InboxItem{
				SessionID:      conv.SessionID,
				State:          conv.State,
				AwaitingReason: conv.AwaitingReason,
				Goal:           conv.Goal,
			})
		}
	}
	return inbox, nil
}

func (s *Service) advanceExecution(ctx context.Context, conv *types.Conversation) (*types.Conversation, error) {
	for i := range conv.Steps {
		step := &conv.Steps[i]
		if step.Status == types.StepDone {
			continue
		}
		if step.RequiresApproval {
			conv.State = types.StateBlocked
			conv.AwaitingReason = fmt.Sprintf("Awaiting manual approval for step %s", step.Title)
			if err := s.store.Save(ctx, conv); err != nil {
				return nil, err
			}
			return conv, nil
		}
		step.Status = types.StepInProgress
		step.StartedAt = s.clock()
		execPrompt := fmt.Sprintf("Goal: %s\nStep: %s\nYou are executing a plan step. Propose at most one shell command to run as `COMMAND: <cmd>` if needed, otherwise return SUCCESS: <result> or BLOCKED: <reason>. Do not execute the command yourself.", conv.Goal, step.Title)
		reply, raw, newSession, duration, err := s.model.Send(ctx, conv.SessionID, execPrompt)
		conv.SessionID = newSession
		call := types.ModelCall{
			Prompt:     execPrompt,
			RawOutput:  raw,
			Reply:      reply,
			Timestamp:  s.clock(),
			DurationMS: duration,
			SessionID:  newSession,
		}
		conv.ModelCalls = append(conv.ModelCalls, call)
		step.Logs = append(step.Logs, reply)
		step.CompletedAt = s.clock()
		upper := strings.ToUpper(strings.TrimSpace(reply))
		if strings.HasPrefix(upper, "COMMAND:") {
			cmdText := strings.TrimSpace(reply[len("COMMAND:"):])
			step.PendingCommand = cmdText
			step.Status = types.StepBlocked
			conv.State = types.StateBlocked
			conv.AwaitingReason = "Awaiting approval to run: " + cmdText
			if saveErr := s.store.Save(ctx, conv); saveErr != nil {
				return nil, saveErr
			}
			return conv, nil
		}
		if err != nil || strings.HasPrefix(upper, "BLOCKED") || strings.HasPrefix(upper, "ERROR") {
			step.Status = types.StepBlocked
			conv.State = types.StateBlocked
			if err != nil {
				conv.AwaitingReason = fmt.Sprintf("Execution blocked: %v", err)
			} else {
				conv.AwaitingReason = "Execution blocked: " + reply
			}
			if saveErr := s.store.Save(ctx, conv); saveErr != nil {
				return nil, saveErr
			}
			return conv, nil
		}
		step.Status = types.StepDone
		conv.State = types.StateExecuting
		conv.AwaitingReason = ""
		if err := s.store.Save(ctx, conv); err != nil {
			return nil, err
		}
	}
	conv.State = types.StateCompleted
	conv.AwaitingReason = ""
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

func parsePlan(plan string) []types.Step {
	lines := strings.Split(plan, "\n")
	steps := []types.Step{}
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		steps = append(steps, types.Step{
			ID:               fmt.Sprintf("step-%d", len(steps)+1),
			Title:            text,
			Status:           types.StepPending,
			RequiresApproval: false, // stub: manual approval gate not implemented yet
			Logs:             []string{},
		})
		if i > 10 {
			// avoid huge plans by default
			break
		}
	}
	return steps
}

func seedPrompt(goal string) string {
	return "You are an execution planner. Given a goal, produce a concise numbered plan (one step per line) and keep it short.\nGoal: " + goal + "\nPlan:"
}
