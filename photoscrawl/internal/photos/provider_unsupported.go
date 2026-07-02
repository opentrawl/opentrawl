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
	return LibrarySnapshot{}, errors.New("Photos sync is only supported on Darwin")
}
