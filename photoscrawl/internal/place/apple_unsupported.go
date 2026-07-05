//go:build !darwin

package place

import (
	"context"
	"errors"
)

func applePlaceContext(context.Context, Input, float64) (Result, error) {
	return Result{}, errors.New("apple place context requires macOS")
}
