package cli

import (
	"os"
	"strconv"

	"golang.org/x/term"
)

// defaultOutputWidth is used when stdout is not a terminal and no
// COLUMNS override is set: wide enough to be readable, narrow enough
// that piped output never sprawls.
const defaultOutputWidth = 120

// outputWidth is the line budget every human table fits inside. A real
// terminal answers for itself; otherwise COLUMNS (useful for agents and
// tests) or the default.
func outputWidth() int {
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && width >= 40 {
		return width
	}
	if columns, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && columns >= 40 {
		return columns
	}
	return defaultOutputWidth
}
