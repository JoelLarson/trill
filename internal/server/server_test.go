package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"trill/internal/service"
	"trill/internal/store"
	"trill/internal/types"
)

type scriptedResponse struct {
	reply     string
	raw       string
	sessionID string
	duration  int64
	err       error
}

// scriptedModel is a deterministic codex.Client double that returns queued replies.
type scriptedModel struct {
	mu        sync.Mutex
	responses []scriptedResponse
	prompts   []string
}

func (m *scriptedModel) Send(ctx context.Context, sessionID, prompt string) (string, string, string, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.responses) == 0 {
		return "", "", sessionID, 0, context.DeadlineExceeded
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	m.prompts = append(m.prompts, prompt)
	if resp.sessionID == "" {
		if sessionID != "" {
			resp.sessionID = sessionID
		} else {
			resp.sessionID = "sess-scripted"
		}
	}
	return resp.reply, resp.raw, resp.sessionID, resp.duration, resp.err
}

type apiHarness struct {
	handler http.Handler
}

func newAPIHarness(model *scriptedModel) *apiHarness {
	mux := http.NewServeMux()
	svc := service.New(store.NewMemoryStore(), model, nil)
	New(svc).RegisterMux(mux)
	return &apiHarness{handler: mux}
}

func (a *apiHarness) postJSON(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	a.handler.ServeHTTP(rr, req)
	return rr.Result()
}

func (a *apiHarness) get(t *testing.T, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	a.handler.ServeHTTP(rr, req)
	return rr.Result()
}

func TestCreateListGetFlow(t *testing.T) {
	model := &scriptedModel{
		responses: []scriptedResponse{{
			reply:     "1) plan step",
			raw:       "raw-plan",
			sessionID: "sess-1",
			duration:  42,
		}},
	}
	api := newAPIHarness(model)

	createResp := api.postJSON(t, "/conversation/create", map[string]string{"prompt": "Ship feature"})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", createResp.StatusCode)
	}
	var created types.Conversation
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.SessionID == "" || created.SessionID != "sess-1" {
		t.Fatalf("unexpected session id: %q", created.SessionID)
	}
	if created.State != types.StateAwaitingPlanApproval {
		t.Fatalf("state = %s, want awaiting_plan_approval", created.State)
	}
	if created.AwaitingReason == "" {
		t.Fatalf("expected awaiting reason to be set")
	}
	if created.PlanText != "1) plan step" || len(created.Steps) != 1 {
		t.Fatalf("plan not persisted: %#v", created.PlanText)
	}

	listResp := api.get(t, "/list")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", listResp.StatusCode)
	}
	var ids []string
	if err := json.NewDecoder(listResp.Body).Decode(&ids); err != nil {
		t.Fatalf("decode ids: %v", err)
	}
	if len(ids) != 1 || ids[0] != "sess-1" {
		t.Fatalf("list ids = %v", ids)
	}

	getResp := api.get(t, "/conversation?id=sess-1")
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", getResp.StatusCode)
	}
	var fetched types.Conversation
	if err := json.NewDecoder(getResp.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	if fetched.Prompt != "Ship feature" || fetched.PlanVersion != 1 {
		t.Fatalf("unexpected conversation: %+v", fetched)
	}

	inboxResp := api.get(t, "/inbox")
	var inbox []types.InboxItem
	if err := json.NewDecoder(inboxResp.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if len(inbox) != 1 || inbox[0].State != types.StateAwaitingPlanApproval {
		t.Fatalf("inbox items = %+v", inbox)
	}
}

func TestApprovePlanCompletesExecution(t *testing.T) {
	model := &scriptedModel{
		responses: []scriptedResponse{
			{reply: "1) verify", raw: "raw-plan", sessionID: "sess-2"},
			{reply: "SUCCESS: done", raw: "raw-exec", sessionID: "sess-2", duration: 25},
		},
	}
	api := newAPIHarness(model)

	createResp := api.postJSON(t, "/conversation/create", map[string]string{"prompt": "Finish milestone"})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", createResp.StatusCode)
	}
	var created types.Conversation
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	approveResp := api.postJSON(t, "/conversation/approve-plan", map[string]string{"id": created.SessionID})
	if approveResp.StatusCode != http.StatusOK {
		t.Fatalf("approve status = %d", approveResp.StatusCode)
	}
	var updated types.Conversation
	if err := json.NewDecoder(approveResp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode approve: %v", err)
	}
	if updated.State != types.StateCompleted {
		t.Fatalf("state = %s, want completed", updated.State)
	}
	if updated.CompletedMessage == "" || !strings.Contains(updated.CompletedMessage, "Last response") {
		t.Fatalf("completed message missing: %q", updated.CompletedMessage)
	}
	if len(updated.Steps) == 0 || updated.Steps[0].Status != types.StepDone {
		t.Fatalf("steps not finished: %+v", updated.Steps)
	}
	if len(updated.ModelCalls) != 2 {
		t.Fatalf("expected two model calls (plan + exec), got %d", len(updated.ModelCalls))
	}

	inboxResp := api.get(t, "/inbox")
	var inbox []types.InboxItem
	if err := json.NewDecoder(inboxResp.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	if len(inbox) != 1 || inbox[0].State != types.StateCompleted {
		t.Fatalf("completed conversation should surface in inbox for auditing, got %+v", inbox)
	}
}

func TestSendCreatesChatConversation(t *testing.T) {
	model := &scriptedModel{
		responses: []scriptedResponse{
			{reply: "pong", raw: "raw-chat", sessionID: "chat-1", duration: 12},
		},
	}
	api := newAPIHarness(model)

	sendResp := api.postJSON(t, "/send", map[string]string{"id": "", "message": "ping"})
	if sendResp.StatusCode != http.StatusOK {
		t.Fatalf("send status = %d", sendResp.StatusCode)
	}
	var call types.ModelCall
	if err := json.NewDecoder(sendResp.Body).Decode(&call); err != nil {
		t.Fatalf("decode call: %v", err)
	}
	if call.Reply != "pong" || call.SessionID != "chat-1" {
		t.Fatalf("unexpected call response: %+v", call)
	}
	if call.DurationMS == 0 {
		t.Fatalf("duration should be set")
	}

	convResp := api.get(t, "/conversation?id=chat-1")
	var conv types.Conversation
	if err := json.NewDecoder(convResp.Body).Decode(&conv); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	if len(conv.Messages) != 2 || conv.Messages[0].Content != "ping" || conv.Messages[1].Content != "pong" {
		t.Fatalf("messages not persisted: %+v", conv.Messages)
	}
	if len(conv.ModelCalls) != 1 {
		t.Fatalf("expected one model call, got %d", len(conv.ModelCalls))
	}
	if conv.State != "" {
		t.Fatalf("chat-only flow should not set execution state, got %s", conv.State)
	}

	if remaining := len(model.responses); remaining != 0 {
		t.Fatalf("expected all scripted responses to be consumed, remaining=%d", remaining)
	}
	// Ensure prompts captured for traceability.
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.prompts) != 1 || !strings.Contains(model.prompts[0], "ping") {
		t.Fatalf("unexpected prompts sent: %v", model.prompts)
	}
}
