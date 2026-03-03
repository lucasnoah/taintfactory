package discord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Cursor tracks the last pipeline event ID sent to Discord.
type Cursor struct {
	LastEventID int    `json:"last_event_id"`
	path        string // file this cursor was loaded from
}

// LoadCursor reads the cursor from path. If the file does not exist, a zeroed
// cursor is returned so the poller starts from the beginning.
func LoadCursor(path string) (*Cursor, error) {
	c := &Cursor{path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cursor file: %w", err)
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse cursor file: %w", err)
	}
	return c, nil
}

// Save writes the cursor to its file, creating parent directories as needed.
func (c *Cursor) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("create cursor dir: %w", err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal cursor: %w", err)
	}
	if err := os.WriteFile(c.path, data, 0o644); err != nil {
		return fmt.Errorf("write cursor file: %w", err)
	}
	return nil
}
