package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// Client sends prompts to Codex, optionally resuming a session.
type Client interface {
	Send(ctx context.Context, sessionID, prompt string) (reply string, raw string, newSessionID string, durationMS int64, err error)
}

type CLIClient struct {
	Timeout time.Duration
}

func NewCLIClient() *CLIClient {
	return &CLIClient{Timeout: 60 * time.Second}
}

func (c *CLIClient) Send(ctx context.Context, sessionID, prompt string) (string, string, string, int64, error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	args := []string{"exec", "--json", "--skip-git-repo-check"}
	if sessionID != "" {
		args = append(args, "resume", sessionID, prompt)
	} else {
		args = append(args, prompt)
	}
	cmd := exec.CommandContext(ctx, "codex", args...)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	duration := time.Since(start).Milliseconds()
	raw := string(out)
	if err != nil {
		return "", raw, sessionID, duration, fmt.Errorf("codex error: %w, output: %s", err, raw)
	}
	threadID, reply, parseErr := parseCodexJSON(out)
	if parseErr != nil {
		return "", raw, sessionID, duration, fmt.Errorf("failed to parse codex output: %w, output: %s", parseErr, raw)
	}
	if threadID == "" {
		threadID = sessionID
	}
	if threadID == "" {
		return "", raw, sessionID, duration, fmt.Errorf("missing session id from codex output")
	}
	return reply, raw, threadID, duration, nil
}

func parseCodexJSON(out []byte) (string, string, error) {
	var sessionID string
	var reply string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Bytes()
		var evt struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		if evt.ThreadID != "" {
			sessionID = evt.ThreadID
		}
		if evt.Type == "item.completed" && evt.Item.Type == "agent_message" && evt.Item.Text != "" {
			reply = evt.Item.Text
		}
	}
	if err := scanner.Err(); err != nil {
		return sessionID, reply, err
	}
	if reply == "" {
		return sessionID, reply, fmt.Errorf("no agent reply found in codex output")
	}
	return sessionID, reply, nil
}
