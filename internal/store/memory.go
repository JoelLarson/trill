package store

import (
	"context"
	"fmt"
	"sync"

	"trill/internal/types"
)

// MemoryStore keeps conversations in memory; thread-safe.
type MemoryStore struct {
	mu    sync.RWMutex
	convs map[string]*types.Conversation
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{convs: make(map[string]*types.Conversation)}
}

func (m *MemoryStore) Save(ctx context.Context, conv *types.Conversation) error {
	if conv == nil || conv.SessionID == "" {
		return fmt.Errorf("conversation missing session id")
	}
	m.mu.Lock()
	m.convs[conv.SessionID] = cloneConversation(conv)
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) Get(ctx context.Context, sessionID string) (*types.Conversation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	conv, ok := m.convs[sessionID]
	if !ok {
		return nil, fmt.Errorf("conversation %s not found", sessionID)
	}
	return cloneConversation(conv), nil
}

func (m *MemoryStore) ListIDs(ctx context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.convs))
	for id := range m.convs {
		ids = append(ids, id)
	}
	return ids, nil
}

func (m *MemoryStore) Delete(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	delete(m.convs, sessionID)
	m.mu.Unlock()
	return nil
}

func cloneConversation(c *types.Conversation) *types.Conversation {
	if c == nil {
		return nil
	}
	msgs := make([]types.Message, len(c.Messages))
	copy(msgs, c.Messages)
	calls := make([]types.ModelCall, len(c.ModelCalls))
	copy(calls, c.ModelCalls)
	return &types.Conversation{
		SessionID:  c.SessionID,
		Messages:   msgs,
		ModelCalls: calls,
	}
}
