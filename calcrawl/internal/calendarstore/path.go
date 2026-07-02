package calendarstore

import (
	"os"
	"path/filepath"
)

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join("Library", "Group Containers", "group.com.apple.calendar", "Calendar.sqlitedb")
	}
	return filepath.Join(home, "Library", "Group Containers", "group.com.apple.calendar", "Calendar.sqlitedb")
}
