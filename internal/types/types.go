package types

import "time"

// Message represents a single chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ConversationState string

const (
	StatePlanning             ConversationState = "planning"
	StateAwaitingPlanApproval ConversationState = "awaiting_plan_approval"
	StateExecuting            ConversationState = "executing"
	StateBlocked              ConversationState = "blocked"
	StateCompleted            ConversationState = "completed"
	StateAborted              ConversationState = "aborted"
)

type StepStatus string

const (
	StepPending    StepStatus = "pending"
	StepInProgress StepStatus = "in_progress"
	StepDone       StepStatus = "done"
	StepFailed     StepStatus = "failed"
	StepBlocked    StepStatus = "blocked"
)

type Step struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Status           StepStatus `json:"status"`
	RequiresApproval bool       `json:"requires_approval"`
	PendingCommand   string     `json:"pending_command"`
	Logs             []string   `json:"logs"`
	StartedAt        time.Time  `json:"started_at"`
	CompletedAt      time.Time  `json:"completed_at"`
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

// Artifact represents cached context or command output that can be reused later.
type Artifact struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
}

// Conversation stores the persisted chat context for a Codex session.
type Conversation struct {
	SessionID        string            `json:"session_id"`
	Prompt           string            `json:"prompt"`
	State            ConversationState `json:"state"`
	PlanVersion      int               `json:"plan_version"`
	PlanText         string            `json:"plan_text"`
	AwaitingReason   string            `json:"awaiting_reason"`
	Steps            []Step            `json:"steps"`
	Messages         []Message         `json:"messages"`
	ModelCalls       []ModelCall       `json:"model_calls"`
	Artifacts        []Artifact        `json:"artifacts"`
	CompletedMessage string            `json:"completed_message"`
	CompletedAt      time.Time         `json:"completed_at"`
}

// InboxItem summarizes items needing attention.
type InboxItem struct {
	SessionID      string            `json:"session_id"`
	Prompt         string            `json:"prompt"`
	State          ConversationState `json:"state"`
	AwaitingReason string            `json:"awaiting_reason"`
}
