package wal

import (
	"io"
	"os"
	"path/filepath"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/filecopy"
)

type StateCopy struct {
	Path string
	root string
}

func Materialize(basePath string, walData []byte, offset int64) (StateCopy, error) {
	root, err := os.MkdirTemp("", "opentrawl-notes-wal-state-*")
	if err != nil {
		return StateCopy{}, err
	}
	target := filepath.Join(root, filepath.Base(basePath))
	if err := filecopy.CopyFile(basePath, target); err != nil {
		_ = os.RemoveAll(root)
		return StateCopy{}, err
	}
	if offset > 0 {
		if offset > int64(len(walData)) {
			_ = os.RemoveAll(root)
			return StateCopy{}, io.ErrUnexpectedEOF
		}
		if err := os.WriteFile(target+"-wal", walData[:offset], 0o600); err != nil {
			_ = os.RemoveAll(root)
			return StateCopy{}, err
		}
	}
	return StateCopy{Path: target, root: root}, nil
}

func (s StateCopy) Close() error {
	if s.root == "" {
		return nil
	}
	return os.RemoveAll(s.root)
}
