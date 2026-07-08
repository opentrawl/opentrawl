// Command photoscrawl-fetch downloads full-resolution originals from iCloud via
// PhotoKit for specific asset UUIDs. Temporary demo helper.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openclaw/photoscrawl/internal/photos"
)

func main() {
	args := os.Args[1:]
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: photoscrawl-fetch <destDir> <uuid> [uuid...]")
		os.Exit(1)
	}
	dest := args[0]
	if err := os.MkdirAll(dest, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx := context.Background()
	fails := 0
	for _, u := range args[1:] {
		out := filepath.Join(dest, u+".orig")
		if err := photos.ExportOriginalResource(ctx, u, out, true); err != nil {
			fmt.Printf("FAIL %s: %v\n", u, err)
			fails++
			continue
		}
		fi, _ := os.Stat(out)
		fmt.Printf("OK %s -> %s (%d bytes)\n", u, out, fi.Size())
	}
	if fails > 0 {
		os.Exit(2)
	}
}
