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

func (unsupportedProvider) Snapshot(context.Context, string) (LibrarySnapshot, error) {
	return LibrarySnapshot{}, errors.New("photos sync is only supported on Darwin")
}
