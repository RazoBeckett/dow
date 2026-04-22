package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Status represents the lifecycle state of a download.
type Status string

const (
	StatusDownloading Status = "downloading"
	StatusComplete    Status = "complete"
	StatusCancelled   Status = "cancelled"
	StatusFailed      Status = "failed"
	StatusPaused      Status = "paused"
)

// Download holds the metadata and live state of a single download.
type Download struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	Filename   string    `json:"filename"`
	Path       string    `json:"path"`
	Status     Status    `json:"status"`
	Progress   float64   `json:"progress"`             // 0–100
	Speed      float64   `json:"speed"`                // bytes/sec (live, while downloading)
	Size       int64     `json:"size"`                 // total bytes; -1 if unknown
	Downloaded int64     `json:"downloaded"`           // bytes received so far
	ETA        int64     `json:"eta"`                 // Unix timestamp (seconds), estimated completion; 0 if unknown
	CancelRequested bool    `json:"cancel_requested,omitempty"`
	PauseRequested  bool    `json:"pause_requested,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// appState is the root structure persisted to disk.
type appState struct {
	Downloads []Download `json:"downloads"`
}

// generateShortID returns a unique 6-character lowercase hex ID (e.g. "3f9a2c")
// that does not collide with any existing download in the list.
// The format intentionally mirrors git short-hashes.
func generateShortID(downloads []Download) string {
	taken := make(map[string]bool, len(downloads))
	for _, d := range downloads {
		taken[d.ID] = true
	}
	for {
		b := make([]byte, 3) // 3 bytes → 6 hex chars
		if _, err := rand.Read(b); err != nil {
			panic("crypto/rand unavailable: " + err.Error())
		}
		id := fmt.Sprintf("%02x%02x%02x", b[0], b[1], b[2])
		if !taken[id] {
			return id
		}
	}
}

// statePath returns the path to the JSON state file and ensures the directory exists.
func statePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory: %w", err)
	}
	dir := filepath.Join(home, ".local", "share", "dow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	return filepath.Join(dir, "state.json"), nil
}

// readState loads the state from disk. Returns an empty state if no file exists yet.
func readState() (*appState, error) {
	p, err := statePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &appState{Downloads: []Download{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s appState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	if s.Downloads == nil {
		s.Downloads = []Download{}
	}
	return &s, nil
}

// writeState persists the state to disk atomically (write tmp → rename).
func writeState(s *appState) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write state tmp: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("commit state: %w", err)
	}
	return nil
}

// withState reads the state, calls fn, then writes it back.
// fn may mutate the state; if fn returns an error the write is skipped.
func withState(fn func(*appState) error) error {
	s, err := readState()
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	return writeState(s)
}

// patchDownload finds the download with the given id inside s and calls fn on it.
// It also bumps UpdatedAt automatically.
func patchDownload(s *appState, id string, fn func(*Download)) {
	for i := range s.Downloads {
		if s.Downloads[i].ID == id {
			fn(&s.Downloads[i])
			s.Downloads[i].UpdatedAt = time.Now()
			return
		}
	}
}
