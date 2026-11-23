//go:build !unix

package semantic

import (
	"fmt"
	"io"
	"os"
)

type mappedSemanticVectorFile struct {
	data []byte
}

func mapSemanticVectorFileReadOnly(file *os.File, fileSizeBytes int64) (*mappedSemanticVectorFile, error) {
	if fileSizeBytes <= 0 {
		return nil, fmt.Errorf("semantic vector cache file is empty")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read semantic vector cache: %w", err)
	}
	return &mappedSemanticVectorFile{data: data}, nil
}

func (m *mappedSemanticVectorFile) Close() error {
	if m == nil {
		return nil
	}
	m.data = nil
	return nil
}
