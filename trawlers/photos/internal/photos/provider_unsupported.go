//go:build !darwin

package photos

import (
	"context"
	"errors"
)

func NewProvider() Provider {
	return unsupportedProvider{}
}

type unsupportedProvider struct{}

func (unsupportedProvider) OpenSnapshot(context.Context, SnapshotRequest) (SourceSnapshot, error) {
	return nil, errors.New("photos updates require macOS")
}
