//go:build unix

package semantic

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type mappedSemanticVectorFile struct {
	data []byte
}

func mapSemanticVectorFileReadOnly(file *os.File, fileSizeBytes int64) (*mappedSemanticVectorFile, error) {
	if fileSizeBytes <= 0 {
		return nil, fmt.Errorf("semantic vector cache file is empty")
	}
	mapped, err := unix.Mmap(int(file.Fd()), 0, int(fileSizeBytes), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap semantic vector cache: %w", err)
	}
	// Pre-populate page-table entries and hint sequential readahead while the
	// query embedding RPC is in flight on the GPU.
	_ = unix.Madvise(mapped, unix.MADV_WILLNEED)
	return &mappedSemanticVectorFile{data: mapped}, nil
}

func (m *mappedSemanticVectorFile) Close() error {
	if m == nil || m.data == nil {
		return nil
	}
	err := unix.Munmap(m.data)
	m.data = nil
	return err
}
