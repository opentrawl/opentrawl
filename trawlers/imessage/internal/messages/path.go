package messages

import (
	"os"
	"path/filepath"
)

func DefaultChatDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join("Library", "Messages", "chat.db")
	}
	return filepath.Join(home, "Library", "Messages", "chat.db")
}
