//go:build !darwin

package applelibrary

import "errors"

func decompressAppleLZFSE([]byte, int) ([]byte, error) {
	return nil, errors.New("only macOS can decompress the recognised text in Apple Photos")
}
