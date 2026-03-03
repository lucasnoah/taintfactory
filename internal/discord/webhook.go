package discord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// Post sends a WebhookPayload to a Discord webhook URL.
// If threadID is non-empty, posts to that thread via ?thread_id= query param.
func Post(webhookURL string, payload WebhookPayload, threadID string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := webhookURL
	if threadID != "" {
		url = webhookURL + "?thread_id=" + threadID
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(data)) //nolint:noctx
	if err != nil {
		return fmt.Errorf("posting to Discord: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Discord returned %d", resp.StatusCode)
	}
	return nil
}
