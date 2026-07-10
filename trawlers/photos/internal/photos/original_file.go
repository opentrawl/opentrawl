package photos

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

func InspectOriginalFile(path string) (os.FileInfo, [sha256.Size]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, [sha256.Size]byte{}, errors.New("original output is not a non-empty regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, [sha256.Size]byte{}, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return info, digest, nil
}
