//go:build !darwin

package photos

import (
	"context"
	"errors"
)

func ExportOriginalResourceThroughApp(ctx context.Context, query OriginalExportQuery, destinationPath string, allowNetwork bool) error {
	return errors.New("signed Photos original fetch app requires macOS")
}
