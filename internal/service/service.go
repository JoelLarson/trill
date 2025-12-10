package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"trill/internal/codex"
	"trill/internal/obs"
	"trill/internal/store"
	"trill/internal/types"
)

type Service struct {
	store store.ConversationStore
	model codex.Client
	obs   *obs.Broker
	clock func() time.Time
}

func New(store store.ConversationStore, model codex.Client, broker *obs.Broker) *Service {
	return &Service{
		store: store,
		model: model,
		obs:   broker,
		clock: time.Now,
	}
}

// Start returns an empty id for compatibility with legacy clients.
func (s *Service) Start(ctx context.Context) (string, error) {
	return "", nil
}

// CreateConversation seeds a plan and moves to awaiting plan approval.
func (s *Service) CreateConversation(ctx context.Context, prompt string) (*types.Conversation, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	planPrompt := seedPrompt(prompt)
	reply, raw, sessionID, duration, err := s.model.Send(ctx, "", planPrompt)
	if err != nil {
		return nil, err
	}
	steps, acceptance := parsePlanAndCriteria(reply)
	conv := &types.Conversation{
		SessionID:          sessionID,
		Prompt:             prompt,
		State:              types.StateAwaitingPlanApproval,
		PlanVersion:        1,
		PlanText:           reply,
		AcceptanceCriteria: acceptance,
		AwaitingReason:     "Awaiting plan approval",
		Steps:              steps,
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
	s.emit(obs.Event{
		Type:        "plan",
		SessionID:   sessionID,
		Prompt:      prompt,
		ModelPrompt: planPrompt,
		PlanText:    reply,
		RawOutput:   raw,
	})
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
	if conv.State != types.StateBlocked && conv.State != types.StateAwaitingInfo && conv.State != types.StateAwaitingStepApproval && conv.State != types.StateAwaitingCommand && conv.State != types.StateReplanning {
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
	s.emit(obs.Event{
		Type:        "chat",
		SessionID:   newSessionID,
		Prompt:      msg,
		ModelPrompt: msg,
		Reply:       reply,
		RawOutput:   raw,
	})
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
	pending := target.PendingCommand
	cmdCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", pending)
	out, err := cmd.CombinedOutput()
	output := string(out)
	target.Logs = append(target.Logs, "EXEC: "+pending, output)
	target.PendingCommand = ""
	artifact := s.addArtifact(conv, "Command output", fmt.Sprintf("Output for `%s`", pending), output, pending)
	if err != nil {
		target.Status = types.StepBlocked
		conv.State = types.StateBlocked
		conv.AwaitingReason = fmt.Sprintf("Command failed: %v", err)
		_ = s.store.Save(ctx, conv)
		s.emit(obs.Event{
			Type:       "command",
			SessionID:  conv.SessionID,
			StepID:     target.ID,
			StepTitle:  target.Title,
			Command:    pending,
			RawOutput:  output,
			Note:       "ERROR: " + err.Error(),
			ArtifactID: artifact.ID,
		})
		return conv, nil
	}
	target.Status = types.StepDone
	target.CompletedAt = s.clock()
	conv.State = types.StateExecuting
	conv.AwaitingReason = ""
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	s.emit(obs.Event{
		Type:       "command",
		SessionID:  conv.SessionID,
		StepID:     target.ID,
		StepTitle:  target.Title,
		Command:    pending,
		RawOutput:  output,
		Note:       "SUCCESS",
		ArtifactID: artifact.ID,
	})
	return s.advanceExecution(ctx, conv)
}

func (s *Service) PlanAndExecute(ctx context.Context, prompt string) (string, error) {
	conv, err := s.CreateConversation(ctx, prompt)
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
		item := types.InboxItem{
			SessionID:        conv.SessionID,
			State:            conv.State,
			AwaitingReason:   conv.AwaitingReason,
			Prompt:           conv.Prompt,
			CompletedMessage: conv.CompletedMessage,
			CompletedAt:      conv.CompletedAt,
		}
		switch conv.State {
		case types.StateAwaitingPlanApproval:
			inbox = append(inbox, item)
		case types.StateAwaitingCommand, types.StateBlocked:
			var pendingStep *types.Step
			for i := range conv.Steps {
				if conv.Steps[i].PendingCommand != "" {
					pendingStep = &conv.Steps[i]
					break
				}
			}
			if pendingStep != nil {
				item.StepID = pendingStep.ID
				item.StepTitle = pendingStep.Title
				item.PendingCommand = pendingStep.PendingCommand
				inbox = append(inbox, item)
			}
		case types.StateAwaitingInfo:
			for i := range conv.Steps {
				if conv.Steps[i].PendingInfo != "" || conv.Steps[i].PendingDependency != "" {
					item.StepID = conv.Steps[i].ID
					item.StepTitle = conv.Steps[i].Title
					item.PendingInfo = conv.Steps[i].PendingInfo
					item.PendingDependency = conv.Steps[i].PendingDependency
					inbox = append(inbox, item)
					break
				}
			}
		case types.StateAwaitingStepApproval:
			inbox = append(inbox, item)
		case types.StateReplanning:
			inbox = append(inbox, item)
		case types.StateCompleted:
			if conv.CompletedMessage != "" {
				inbox = append(inbox, item)
			}
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
			conv.State = types.StateAwaitingStepApproval
			conv.AwaitingReason = fmt.Sprintf("Awaiting manual approval for step %s", step.Title)
			if err := s.store.Save(ctx, conv); err != nil {
				return nil, err
			}
			return conv, nil
		}
		step.Status = types.StepInProgress
		step.StartedAt = s.clock()
		contextLogs := summarizeLogs(conv, 5)
		execPrompt := fmt.Sprintf("Prompt: %s\nPlan: %s\nAcceptance criteria: %s\nRecent context:\n%s\nStep: %s\nYou are executing a plan step. Respond with one of:\n- COMMAND: <cmd> (shell command suggestion, do not execute)\n- NEED: <missing info>\n- DEPENDENCY: <what must be installed or prepared>\n- SUCCESS: <result>\n- BLOCKED: <reason>\nKeep it concise and actionable.", conv.Prompt, conv.PlanText, strings.Join(conv.AcceptanceCriteria, "; "), contextLogs, step.Title)
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
		stepEvent := obs.Event{
			Type:        "step",
			SessionID:   newSession,
			Prompt:      conv.Prompt,
			ModelPrompt: execPrompt,
			StepID:      step.ID,
			StepTitle:   step.Title,
			RawOutput:   raw,
			Reply:       reply,
		}
		upper := strings.ToUpper(strings.TrimSpace(reply))
		if strings.HasPrefix(upper, "COMMAND:") {
			cmdText := strings.TrimSpace(reply[len("COMMAND:"):])
			step.PendingCommand = cmdText
			step.Status = types.StepBlocked
			conv.State = types.StateAwaitingCommand
			conv.AwaitingReason = "Awaiting approval to run: " + cmdText
			stepEvent.Command = cmdText
			stepEvent.Note = "COMMAND_REQUEST"
			s.emit(stepEvent)
			if saveErr := s.store.Save(ctx, conv); saveErr != nil {
				return nil, saveErr
			}
			return conv, nil
		}
		if strings.HasPrefix(upper, "NEED:") {
			info := strings.TrimSpace(reply[len("NEED:"):])
			cmd, cmdCall := s.proposeDiscoveryCommand(ctx, conv, info, "info")
			if cmdCall != nil {
				conv.ModelCalls = append(conv.ModelCalls, *cmdCall)
			}
			if cmd != "" {
				step.PendingCommand = cmd
				step.Status = types.StepBlocked
				conv.State = types.StateAwaitingCommand
				conv.AwaitingReason = "Awaiting approval to gather info: " + info
				stepEvent.Command = cmd
				stepEvent.Note = "INFO_COMMAND_REQUEST"
				s.emit(stepEvent)
				if saveErr := s.store.Save(ctx, conv); saveErr != nil {
					return nil, saveErr
				}
				return conv, nil
			}
			step.PendingInfo = info
			step.Status = types.StepBlocked
			conv.State = types.StateAwaitingInfo
			conv.AwaitingReason = "Needs info: " + info
			stepEvent.Note = conv.AwaitingReason
			s.emit(stepEvent)
			if saveErr := s.store.Save(ctx, conv); saveErr != nil {
				return nil, saveErr
			}
			return conv, nil
		}
		if strings.HasPrefix(upper, "DEPENDENCY:") {
			dep := strings.TrimSpace(reply[len("DEPENDENCY:"):])
			cmd, cmdCall := s.proposeDiscoveryCommand(ctx, conv, dep, "dependency")
			if cmdCall != nil {
				conv.ModelCalls = append(conv.ModelCalls, *cmdCall)
			}
			if cmd != "" {
				step.PendingCommand = cmd
				step.Status = types.StepBlocked
				conv.State = types.StateAwaitingCommand
				conv.AwaitingReason = "Awaiting approval to satisfy dependency: " + dep
				stepEvent.Command = cmd
				stepEvent.Note = "DEPENDENCY_COMMAND_REQUEST"
				s.emit(stepEvent)
				if saveErr := s.store.Save(ctx, conv); saveErr != nil {
					return nil, saveErr
				}
				return conv, nil
			}
			step.PendingDependency = dep
			step.Status = types.StepBlocked
			conv.State = types.StateAwaitingInfo
			conv.AwaitingReason = "Dependency required: " + dep
			stepEvent.Note = conv.AwaitingReason
			s.emit(stepEvent)
			if saveErr := s.store.Save(ctx, conv); saveErr != nil {
				return nil, saveErr
			}
			return conv, nil
		}
		if err != nil || strings.HasPrefix(upper, "BLOCKED") || strings.HasPrefix(upper, "ERROR") {
			step.Status = types.StepBlocked
			conv.State = types.StateReplanning
			if err != nil {
				conv.AwaitingReason = fmt.Sprintf("Execution blocked: %v", err)
			} else {
				conv.AwaitingReason = "Execution blocked: " + reply
			}
			stepEvent.Note = conv.AwaitingReason
			s.emit(stepEvent)
			if saveErr := s.store.Save(ctx, conv); saveErr != nil {
				return nil, saveErr
			}
			if helperErr := s.resolveBlock(ctx, conv, conv.AwaitingReason, step.Title); helperErr != nil {
				return nil, helperErr
			}
			return conv, nil
		}
		step.Status = types.StepDone
		conv.State = types.StateExecuting
		conv.AwaitingReason = ""
		stepEvent.Note = "SUCCESS"
		s.emit(stepEvent)
		if err := s.store.Save(ctx, conv); err != nil {
			return nil, err
		}
	}
	if len(conv.AcceptanceCriteria) == 0 {
		return s.completeConversation(ctx, conv)
	}
	conv.State = types.StateVerifying
	conv.AwaitingReason = "Verifying acceptance criteria"
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	return s.verifyAcceptance(ctx, conv)
}

func (s *Service) completeConversation(ctx context.Context, conv *types.Conversation) (*types.Conversation, error) {
	conv.State = types.StateCompleted
	conv.AwaitingReason = ""
	finalReply := ""
	if len(conv.ModelCalls) > 0 {
		finalReply = conv.ModelCalls[len(conv.ModelCalls)-1].Reply
	}
	if finalReply == "" && len(conv.Steps) > 0 {
		lastStep := conv.Steps[len(conv.Steps)-1]
		if len(lastStep.Logs) > 0 {
			finalReply = lastStep.Logs[len(lastStep.Logs)-1]
		}
	}
	conv.CompletedMessage = "Plan completed successfully."
	if finalReply != "" {
		conv.CompletedMessage += " Last response: " + finalReply
	}
	conv.CompletedAt = s.clock()
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

func (s *Service) verifyAcceptance(ctx context.Context, conv *types.Conversation) (*types.Conversation, error) {
	checklist := "-"
	if len(conv.AcceptanceCriteria) > 0 {
		checklist = "- " + strings.Join(conv.AcceptanceCriteria, "\n- ")
	}
	verifyPrompt := fmt.Sprintf("Goal: %s\nAcceptance criteria:\n%s\nRecent execution context:\n%s\nRespond with PASS: <short reason> if all criteria are met. If any are missing, respond with FAIL: <gaps> and list missing items.", conv.Prompt, checklist, summarizeLogs(conv, 8))
	reply, raw, sessionID, duration, err := s.model.Send(ctx, conv.SessionID, verifyPrompt)
	if err != nil {
		conv.State = types.StateBlocked
		conv.AwaitingReason = fmt.Sprintf("Verification failed: %v", err)
		_ = s.store.Save(ctx, conv)
		return nil, err
	}
	conv.SessionID = sessionID
	call := types.ModelCall{
		Prompt:     verifyPrompt,
		RawOutput:  raw,
		Reply:      reply,
		Timestamp:  s.clock(),
		DurationMS: duration,
		SessionID:  sessionID,
	}
	conv.ModelCalls = append(conv.ModelCalls, call)
	upper := strings.ToUpper(strings.TrimSpace(reply))
	if strings.HasPrefix(upper, "PASS") || strings.HasPrefix(upper, "SUCCESS") {
		conv.CompletedMessage = "Acceptance criteria satisfied. " + reply
		conv.CompletedAt = s.clock()
		conv.State = types.StateCompleted
		conv.AwaitingReason = ""
		if err := s.store.Save(ctx, conv); err != nil {
			return nil, err
		}
		return conv, nil
	}
	conv.State = types.StateReplanning
	conv.AwaitingReason = "Verification failed: " + reply
	if err := s.store.Save(ctx, conv); err != nil {
		return nil, err
	}
	if err := s.resolveBlock(ctx, conv, reply, "acceptance verification"); err != nil {
		return nil, err
	}
	return conv, nil
}

func (s *Service) proposeDiscoveryCommand(ctx context.Context, conv *types.Conversation, need, kind string) (string, *types.ModelCall) {
	if conv == nil {
		return "", nil
	}
	prompt := fmt.Sprintf("Goal: %s\nNeed: %s\nPlan: %s\nRecent context:\n%s\nSuggest a single shell command to gather the missing %s or unblock the dependency. Respond strictly as `COMMAND: <cmd>` with no explanation and no execution.", conv.Prompt, need, conv.PlanText, summarizeLogs(conv, 5), kind)
	reply, raw, sessionID, duration, err := s.model.Send(ctx, conv.SessionID, prompt)
	call := &types.ModelCall{
		Prompt:     prompt,
		RawOutput:  raw,
		Reply:      reply,
		Timestamp:  s.clock(),
		DurationMS: duration,
		SessionID:  sessionID,
	}
	if err != nil {
		return "", call
	}
	upper := strings.ToUpper(strings.TrimSpace(reply))
	if !strings.HasPrefix(upper, "COMMAND:") {
		return "", call
	}
	cmd := strings.TrimSpace(reply[len("COMMAND:"):])
	return cmd, call
}

func parsePlanAndCriteria(plan string) ([]types.Step, []string) {
	lines := strings.Split(plan, "\n")
	steps := make([]types.Step, 0, len(lines))
	acceptance := make([]string, 0)
	inAcceptance := false
	for i, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		upper := strings.ToUpper(text)
		if strings.HasPrefix(upper, "PLAN:") {
			inAcceptance = false
			continue
		}
		if strings.HasPrefix(upper, "ACCEPTANCE") || strings.HasPrefix(upper, "ACCEPT:") || strings.HasPrefix(upper, "CRITERIA") {
			inAcceptance = true
			if strings.Contains(text, ":") {
				parts := strings.SplitN(text, ":", 2)
				if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
					acceptance = append(acceptance, strings.TrimSpace(parts[1]))
				}
			}
			continue
		}
		if inAcceptance {
			acceptance = append(acceptance, strings.TrimPrefix(text, "- "))
			continue
		}
		steps = append(steps, types.Step{
			ID:               fmt.Sprintf("step-%d", len(steps)+1),
			Title:            text,
			Status:           types.StepPending,
			RequiresApproval: false,
			Logs:             []string{},
		})
		if i > 10 {
			// avoid huge plans by default
			break
		}
	}
	return steps, acceptance
}

func summarizeLogs(conv *types.Conversation, max int) string {
	var entries []string
	for i := len(conv.Steps) - 1; i >= 0 && len(entries) < max; i-- {
		step := conv.Steps[i]
		for j := len(step.Logs) - 1; j >= 0 && len(entries) < max; j-- {
			entries = append(entries, fmt.Sprintf("%s: %s", step.Title, step.Logs[j]))
		}
	}
	if len(entries) == 0 {
		return "None"
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return strings.Join(entries, "\n")
}

func seedPrompt(prompt string) string {
	return "You are an execution planner. Given a prompt, produce a concise numbered plan (one step per line) and also list acceptance criteria as `ACCEPT: <criterion>` lines. Keep both lists short and outcome-focused.\nPrompt: " + prompt + "\nPlan:"
}

func unblockPrompt(goal, stepTitle, reason, planText string) string {
	return fmt.Sprintf("The goal is: %s\nStep %q failed with reason: %s. Provide a concise revised plan (numbered steps) and updated acceptance criteria as `ACCEPT:` lines that help unblock and continue the goal. Keep it short.\nPrevious plan and acceptance criteria:\n%s\nNew Plan:", goal, stepTitle, reason, planText)
}

func (s *Service) resolveBlock(ctx context.Context, conv *types.Conversation, reason, stepTitle string) error {
	prompt := unblockPrompt(conv.Prompt, stepTitle, reason, conv.PlanText)
	reply, raw, sessionID, duration, err := s.model.Send(ctx, conv.SessionID, prompt)
	if err != nil {
		return err
	}
	conv.SessionID = sessionID
	conv.PlanText = reply
	conv.Steps, conv.AcceptanceCriteria = parsePlanAndCriteria(reply)
	conv.PlanVersion++
	conv.State = types.StateAwaitingPlanApproval
	conv.AwaitingReason = "Awaiting plan approval after block"
	call := types.ModelCall{
		Prompt:     prompt,
		RawOutput:  raw,
		Reply:      reply,
		Timestamp:  s.clock(),
		DurationMS: duration,
		SessionID:  sessionID,
	}
	conv.ModelCalls = append(conv.ModelCalls, call)
	if err := s.store.Save(ctx, conv); err != nil {
		return err
	}
	s.emit(obs.Event{
		Type:        "plan",
		SessionID:   conv.SessionID,
		Prompt:      conv.Prompt,
		ModelPrompt: prompt,
		PlanText:    reply,
		RawOutput:   raw,
		Note:        "Block resolution plan",
	})
	return nil
}

func (s *Service) addArtifact(conv *types.Conversation, title, description, content, source string) *types.Artifact {
	if conv == nil {
		return nil
	}
	artifact := types.Artifact{
		ID:          fmt.Sprintf("artifact-%d", time.Now().UnixNano()),
		Title:       title,
		Description: description,
		Content:     content,
		Source:      source,
		CreatedAt:   s.clock(),
	}
	conv.Artifacts = append(conv.Artifacts, artifact)
	return &artifact
}

func (s *Service) emit(ev obs.Event) {
	if s.obs == nil {
		return
	}
	s.obs.Publish(ev)
}
