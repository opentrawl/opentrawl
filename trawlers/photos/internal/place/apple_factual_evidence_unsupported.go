//go:build !darwin

package place

import (
	"context"
	"errors"
)

func acquireAppleFactualLocationEvidence(context.Context, Input, string, float64) (FactualLocationEvidence, error) {
	return FactualLocationEvidence{}, errors.New("Apple factual location operations require macOS")
}
