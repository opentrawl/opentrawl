// Command photoscrawl-inspect runs one Photos source snapshot and index pass
// without exposing source records. It is an internal inspection surface, not a
// second product workflow.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

type inspectConfiguration struct {
	libraryPath       string
	archivePath       string
	snapshotDirectory string
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "photoscrawl-inspect:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	configuration, err := parseInspectConfiguration(arguments, stderr)
	if err != nil {
		return err
	}
	result, err := archive.Update(ctx, archive.Paths{Database: configuration.archivePath}, archive.UpdateOptions{
		LibraryPath: configuration.libraryPath,
		Provider:    photos.SQLiteSnapshotProvider{SnapshotDir: configuration.snapshotDirectory},
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Photos source index complete")
	fmt.Fprintln(stdout, "Source: Photos.sqlite")
	fmt.Fprintf(stdout, "Assets: %d\n", result.AssetsSeen)
	fmt.Fprintf(stdout, "Resources: %d\n", result.ResourcesSeen)
	fmt.Fprintf(stdout, "Album memberships: %d\n", result.AlbumMembershipsSeen)
	fmt.Fprintf(stdout, "Capture locations: %d\n", result.LocationsSeen)
	fmt.Fprintf(stdout, "New assets: %d\n", result.AssetsNew)
	fmt.Fprintf(stdout, "Changed assets: %d\n", result.AssetsChanged)
	fmt.Fprintf(stdout, "Unchanged assets: %d\n", result.AssetsUnchanged)
	fmt.Fprintf(stdout, "Missing assets: %d\n", result.PreviouslySeenMissing)
	return nil
}

func parseInspectConfiguration(arguments []string, stderr io.Writer) (inspectConfiguration, error) {
	flags := flag.NewFlagSet("photoscrawl-inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configuration := inspectConfiguration{}
	flags.StringVar(&configuration.libraryPath, "library", "", "Photos library package to read")
	flags.StringVar(&configuration.archivePath, "archive", "", "private SQLite archive to update")
	flags.StringVar(&configuration.snapshotDirectory, "snapshot-directory", "", "private directory for the temporary SQLite snapshot")
	if err := flags.Parse(arguments); err != nil {
		return inspectConfiguration{}, err
	}
	if flags.NArg() != 0 {
		return inspectConfiguration{}, errors.New("photoscrawl-inspect does not accept positional arguments")
	}
	configuration.libraryPath = strings.TrimSpace(configuration.libraryPath)
	if configuration.libraryPath == "" {
		return inspectConfiguration{}, errors.New("--library is required")
	}
	configuration.archivePath = strings.TrimSpace(configuration.archivePath)
	if configuration.archivePath == "" {
		return inspectConfiguration{}, errors.New("--archive is required")
	}
	absoluteLibraryPath, err := filepath.Abs(configuration.libraryPath)
	if err != nil {
		return inspectConfiguration{}, fmt.Errorf("resolve Photos library path: %w", err)
	}
	absoluteArchivePath, err := filepath.Abs(configuration.archivePath)
	if err != nil {
		return inspectConfiguration{}, fmt.Errorf("resolve archive path: %w", err)
	}
	configuration.libraryPath = absoluteLibraryPath
	configuration.archivePath = absoluteArchivePath
	if configuration.snapshotDirectory != "" {
		absoluteSnapshotDirectory, err := filepath.Abs(configuration.snapshotDirectory)
		if err != nil {
			return inspectConfiguration{}, fmt.Errorf("resolve SQLite snapshot directory: %w", err)
		}
		configuration.snapshotDirectory = absoluteSnapshotDirectory
	}
	return configuration, nil
}
