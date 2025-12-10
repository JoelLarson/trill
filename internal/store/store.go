package store

import (
	"context"

	"agent-manager/internal/types"
)

// ConversationStore persists conversations keyed by session ID.
type ConversationStore interface {
	Save(ctx context.Context, conv *types.Conversation) error
	Get(ctx context.Context, sessionID string) (*types.Conversation, error)
	ListIDs(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, sessionID string) error
}
