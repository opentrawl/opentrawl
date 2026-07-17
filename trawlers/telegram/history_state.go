package telecrawl

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	ckconfig "github.com/opentrawl/opentrawl/trawlkit/config"
)

// telegramHistoryState is an internal restart checkpoint, not a second user
// configuration surface. Public intent lives in Config.FullHistory; this file
// only prevents an interrupted first download from starting over.
type telegramHistoryState struct {
	Complete         bool           `json:"complete"`
	CompletedDialogs []string       `json:"completed_dialogs,omitempty"`
	DialogOffsets    map[string]int `json:"dialog_offsets,omitempty"`
}

func loadTelegramHistoryState(archivePath string) (telegramHistoryState, error) {
	data, err := os.ReadFile(telegramHistoryStatePath(archivePath))
	if errors.Is(err, os.ErrNotExist) {
		return telegramHistoryState{}, nil
	}
	if err != nil {
		return telegramHistoryState{}, fmt.Errorf("read Telegram history checkpoint: %w", err)
	}
	var state telegramHistoryState
	if err := json.Unmarshal(data, &state); err != nil {
		return telegramHistoryState{}, fmt.Errorf("read Telegram history checkpoint: %w", err)
	}
	state.CompletedDialogs = uniqueSortedStrings(state.CompletedDialogs)
	if state.DialogOffsets == nil {
		state.DialogOffsets = map[string]int{}
	}
	return state, nil
}

func saveTelegramHistoryState(archivePath string, state telegramHistoryState) error {
	path := telegramHistoryStatePath(archivePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Telegram state directory: %w", err)
	}
	state.CompletedDialogs = uniqueSortedStrings(state.CompletedDialogs)
	for dialog, offset := range state.DialogOffsets {
		if strings.TrimSpace(dialog) == "" || offset <= 0 {
			delete(state.DialogOffsets, dialog)
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode Telegram history checkpoint: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".history-state-*")
	if err != nil {
		return fmt.Errorf("create Telegram history checkpoint: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Telegram history checkpoint: %w", err)
	}
	return nil
}

func telegramHistoryStatePath(archivePath string) string {
	archivePath = strings.TrimSpace(archivePath)
	if archivePath == "" {
		return "telegram.history-state.json"
	}
	return archivePath + ".history-state.json"
}

func writeTelegramConfig(path string, cfg Config) error {
	path = ckconfig.ExpandHome(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Telegram config directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".telegram-config-*")
	if err != nil {
		return fmt.Errorf("create Telegram config: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := ckconfig.WriteTOML(temporaryPath, &cfg, 0o600); err != nil {
		return err
	}
	written, err := os.Open(temporaryPath)
	if err != nil {
		return err
	}
	if err := written.Sync(); err != nil {
		_ = written.Close()
		return err
	}
	if err := written.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Telegram config: %w", err)
	}
	return nil
}

func (s telegramHistoryState) completedSet() map[string]bool {
	out := make(map[string]bool, len(s.CompletedDialogs))
	for _, dialog := range s.CompletedDialogs {
		out[dialog] = true
	}
	return out
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
