//go:build !darwin

package place

import (
	"context"
	"errors"
)

func callAppleBoundary(_ context.Context, input Input, radius float64) appleBoundaryOutput {
	request, requestErr := appleRequestJSON(input, radius)
	if requestErr != nil {
		return appleBoundaryOutput{Err: requestErr}
	}
	err := errors.New("Apple place evidence requires macOS")
	return appleBoundaryOutput{Request: request, Response: []byte(err.Error()), Err: err}
}
