//go:build !darwin

package applelibrary

import "errors"

func decodeAppleCharacterRecognitionData([]byte) ([]string, error) {
	return nil, errors.New("only macOS can decode the recognised text in Apple Photos")
}
