package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"containersagents.dev/v2/internal/fsutil"
)

type Record struct {
	Timestamp          time.Time      `json:"timestamp"`
	Command            string         `json:"command"`
	EnvironmentID      string         `json:"environmentId,omitempty"`
	EnvironmentName    string         `json:"environmentName,omitempty"`
	PlannedActions     []string       `json:"plannedActions,omitempty"`
	Result             string         `json:"result"`
	Error              string         `json:"error,omitempty"`
	ProfileHash        string         `json:"profileHash,omitempty"`
	SpecHash           string         `json:"specHash,omitempty"`
	ResourceClass      string         `json:"resourceClass,omitempty"`
	SecurityClass      string         `json:"securityClass,omitempty"`
	PodmanResourceIDs  []string       `json:"podmanResourceIds,omitempty"`
	DestructiveConfirm string         `json:"destructiveConfirmation,omitempty"`
	PolicyExceptions   []string       `json:"policyExceptions,omitempty"`
	Details            map[string]any `json:"details,omitempty"`
}

type Logger struct{ MaxBytes int64 }

func (l Logger) Append(path string, record Record) error {
	if l.MaxBytes <= 0 {
		l.MaxBytes = 10 * 1024 * 1024
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	if err := fsutil.EnsureDir(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= l.MaxBytes {
		rotated := path + ".1"
		_ = os.Remove(rotated)
		if err := os.Rename(path, rotated); err != nil {
			return fmt.Errorf("rotate audit log: %w", err)
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode audit record: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write audit log: %w", writeErr)
	}
	return closeErr
}
