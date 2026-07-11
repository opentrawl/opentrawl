// Command photoscrawl-dev-cache is the internal one-shot runner for the
// external development originals cache. It is not part of the trawl surface.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/developmentcache"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

type commandConfig struct {
	ArchivePath         string
	CacheRoot           string
	SourceRoot          string
	StatePath           string
	SourceLibraryID     string
	BatchSize           int
	CapacityBytes       int64
	FreeSpaceFloorBytes int64
	AllowNetwork        bool
}

type runnerDependencies struct {
	newCache       func(commandConfig) (developmentcache.Cache, error)
	openCheckpoint func(context.Context, developmentcache.StoragePaths) (*developmentcache.Checkpoint, error)
	selectAssets   func(context.Context, string, string) (archive.DevelopmentCacheSelection, error)
	availableBytes func(string) (int64, error)
	clock          func() time.Time
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, productionDependencies()))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies runnerDependencies) int {
	config, err := parseConfig(args, stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stderr, err)
		}
		return 2
	}
	if err := execute(ctx, config, stdout, stderr, dependencies); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func parseConfig(args []string, stderr io.Writer) (commandConfig, error) {
	flags := flag.NewFlagSet("photoscrawl-dev-cache", flag.ContinueOnError)
	flags.SetOutput(stderr)
	archivePath := flags.String("archive", "", "read-only Photos archive path")
	cacheRoot := flags.String("cache-root", "", "pre-existing external cache root")
	sourceRoot := flags.String("source-root", "", "Photos library root")
	statePath := flags.String("state", "", "durable checkpoint SQLite path")
	sourceLibraryID := flags.String("source-library-id", "", "explicit Photos source identity")
	batchSize := flags.Int("batch-size", 0, "maximum new checked completions")
	capacityBytes := flags.Int64("capacity-bytes", 0, "external cache capacity ceiling")
	freeSpaceFloorBytes := flags.String("free-space-floor-bytes", "", "required free bytes after a new original")
	network := flags.String("network", "", "network policy: disabled or enabled")
	if err := flags.Parse(args); err != nil {
		return commandConfig{}, err
	}
	if flags.NArg() != 0 {
		return commandConfig{}, errors.New("photoscrawl-dev-cache does not accept positional arguments")
	}
	floor, err := strconv.ParseInt(*freeSpaceFloorBytes, 10, 64)
	if err != nil || floor < 0 {
		return commandConfig{}, errors.New("--free-space-floor-bytes must be a non-negative integer")
	}
	allowNetwork := false
	switch *network {
	case "disabled":
	case "enabled":
		allowNetwork = true
	default:
		return commandConfig{}, errors.New("--network must be explicitly set to disabled or enabled")
	}
	config := commandConfig{
		ArchivePath:         strings.TrimSpace(*archivePath),
		CacheRoot:           strings.TrimSpace(*cacheRoot),
		SourceRoot:          strings.TrimSpace(*sourceRoot),
		StatePath:           strings.TrimSpace(*statePath),
		SourceLibraryID:     strings.TrimSpace(*sourceLibraryID),
		BatchSize:           *batchSize,
		CapacityBytes:       *capacityBytes,
		FreeSpaceFloorBytes: floor,
		AllowNetwork:        allowNetwork,
	}
	if config.ArchivePath == "" || config.CacheRoot == "" || config.SourceRoot == "" || config.StatePath == "" || config.SourceLibraryID == "" {
		return commandConfig{}, errors.New("--archive, --cache-root, --source-root, --state and --source-library-id are required")
	}
	if config.BatchSize <= 0 {
		return commandConfig{}, errors.New("--batch-size must be positive")
	}
	if config.CapacityBytes <= 0 {
		return commandConfig{}, errors.New("--capacity-bytes must be positive")
	}
	return config, nil
}

func execute(ctx context.Context, config commandConfig, stdout, stderr io.Writer, dependencies runnerDependencies) error {
	if dependencies.newCache == nil || dependencies.openCheckpoint == nil || dependencies.selectAssets == nil || dependencies.availableBytes == nil {
		return errors.New("photoscrawl-dev-cache dependencies are not configured")
	}
	paths, err := developmentcache.ValidateStoragePaths(developmentcache.StoragePaths{
		ArchivePath: config.ArchivePath,
		SourceRoot:  config.SourceRoot,
		CacheRoot:   config.CacheRoot,
		StatePath:   config.StatePath,
	})
	if err != nil {
		return err
	}
	config.ArchivePath = paths.ArchivePath
	config.SourceRoot = paths.SourceRoot
	config.CacheRoot = paths.CacheRoot
	config.StatePath = paths.StatePath
	if photos.SourceLibraryID(config.SourceRoot) != config.SourceLibraryID {
		return errors.New("configured source library ID does not match the Photos source root")
	}
	resolver, err := dependencies.newCache(config)
	if err != nil {
		return err
	}
	checkpoint, err := dependencies.openCheckpoint(ctx, paths)
	if err != nil {
		return err
	}
	defer func() { _ = checkpoint.Close() }()
	engine := developmentcache.Engine{
		Select: func(ctx context.Context, sourceLibraryID string) (archive.DevelopmentCacheSelection, error) {
			return dependencies.selectAssets(ctx, config.ArchivePath, sourceLibraryID)
		},
		Cache:      resolver,
		Checkpoint: checkpoint,
		AvailableBytes: func(context.Context) (int64, error) {
			return dependencies.availableBytes(config.CacheRoot)
		},
		Log: func(event developmentcache.Event) {
			_ = json.NewEncoder(stderr).Encode(event)
		},
		Clock: dependencies.clock,
	}
	result, err := engine.Run(ctx, developmentcache.Config{
		SourceLibraryID:     config.SourceLibraryID,
		BatchSize:           config.BatchSize,
		CapacityBytes:       config.CapacityBytes,
		FreeSpaceFloorBytes: config.FreeSpaceFloorBytes,
		AllowNetwork:        config.AllowNetwork,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(result)
}

func productionDependencies() runnerDependencies {
	return runnerDependencies{
		newCache: func(config commandConfig) (developmentcache.Cache, error) {
			return photos.NewDevelopmentOriginalResolver(config.CacheRoot, config.SourceRoot, photos.ExportOriginalResourceThroughApp)
		},
		openCheckpoint: developmentcache.OpenCheckpoint,
		selectAssets:   archive.SelectDevelopmentCacheAssets,
		availableBytes: availableBytes,
	}
}

func availableBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if available > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(available), nil
}
