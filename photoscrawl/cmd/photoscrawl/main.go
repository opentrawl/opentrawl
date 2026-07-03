package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/photoscrawl/internal/archive"
	"github.com/openclaw/photoscrawl/internal/photos"
)

const (
	ollamaCloudBaseURL = "https://ollama.com/api"
	ollamaAPIKeyEnv    = "OLLAMA_API_KEY"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		if wantsJSON(os.Args[1:]) {
			if writeErr := writeError(os.Stdout, err); writeErr != nil {
				fmt.Fprintln(os.Stderr, writeErr)
			}
		} else {
			fmt.Fprintln(os.Stderr, humanError(err))
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) (err error) {
	if len(args) == 0 {
		return usage()
	}
	paths, err := archive.DefaultPaths()
	if err != nil {
		return err
	}
	runLog, err := startLogRun(paths, args[0], wantsJSON(args))
	if err != nil {
		return err
	}
	defer func() {
		err = finishLogRun(runLog, args[0], err)
	}()
	switch args[0] {
	case "metadata":
		fs := flag.NewFlagSet("metadata", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if fs.NArg() != 0 {
			return output.UsageError{Err: errors.New("metadata takes flags only")}
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		manifest := archive.ControlManifest(paths)
		if err := writeMetadata(os.Stdout, format, manifest); err != nil {
			return err
		}
		logInfo(runLog, "metadata_written", fmt.Sprintf("capabilities=%d commands=%d", len(manifest.Capabilities), len(manifest.Commands)))
		return nil
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if *dbPath != "" {
			paths.Database = *dbPath
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		status, err := archive.Status(ctx, paths)
		if err != nil {
			return err
		}
		tail := readLogTail(paths, runLog)
		if err := writeStatus(os.Stdout, format, status, tail); err != nil {
			return err
		}
		logInfo(runLog, "status_written", fmt.Sprintf("state=%s counts=%d", status.State, len(status.Counts)))
		return nil
	case "doctor":
		fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		libraryPath := fs.String("library", "", "Photos Library.photoslibrary path")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if fs.NArg() != 0 {
			return output.UsageError{Err: errors.New("doctor takes flags only")}
		}
		if *dbPath != "" {
			paths.Database = *dbPath
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := archive.Doctor(ctx, paths, archive.DoctorOptions{LibraryPath: *libraryPath})
		if err != nil {
			return err
		}
		tail := readLogTail(paths, runLog)
		if err := writeDoctor(os.Stdout, format, result, tail); err != nil {
			return err
		}
		logInfo(runLog, "doctor_written", fmt.Sprintf("checks=%d failures=%d", len(result.Checks), doctorFailureCount(result.Checks)))
		return nil
	case "sync":
		fs := flag.NewFlagSet("sync", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		libraryPath := fs.String("library", "", "Photos Library.photoslibrary path")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if *dbPath != "" {
			paths.Database = *dbPath
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := archive.Sync(ctx, paths, archive.SyncOptions{
			LibraryPath: *libraryPath,
			Provider:    photos.NewProvider(),
		})
		if err != nil {
			return err
		}
		if err := writeSync(os.Stdout, format, result); err != nil {
			return err
		}
		logInfo(runLog, "sync_written", fmt.Sprintf("provider=%s assets=%d new=%d changed=%d unchanged=%d missing=%d", result.Provider, result.AssetsSeen, result.AssetsNew, result.AssetsChanged, result.AssetsUnchanged, result.PreviouslySeenMissing))
		return nil
	case "classify":
		fs := flag.NewFlagSet("classify", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dbPath := fs.String("db", "", "photos.sqlite path")
		all := fs.Bool("all", false, "classify all pending assets")
		limit := fs.Int("limit", 100, "max pending assets to classify")
		model := fs.String("model", "", "Ollama-API vision model for content observations; local or cloud")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if *dbPath != "" {
			paths.Database = *dbPath
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := archive.Classify(ctx, paths, archive.ClassifyOptions{
			All:         *all,
			Limit:       *limit,
			Model:       *model,
			ModelURL:    ollamaCloudBaseURL,
			ModelKeyEnv: ollamaAPIKeyEnv,
		})
		if err != nil {
			return err
		}
		if err := output.Write(os.Stdout, format, "classify", result); err != nil {
			return err
		}
		logInfo(runLog, "classify_written", fmt.Sprintf("processed=%d metadata=%d content=%d failures=%d", result.Processed, result.MetadataClassified, result.ContentClassified, result.ContentClassificationFailures))
		return nil
	case "search":
		parsed, err := parseSearchCommand(args[1:])
		if err != nil {
			return err
		}
		if parsed.DBPath != "" {
			paths.Database = parsed.DBPath
		}
		result, err := archive.Search(ctx, paths, archive.SearchOptions{Query: parsed.Query, Limit: parsed.Limit, After: parsed.After, Before: parsed.Before})
		if err != nil {
			return err
		}
		if err := writeSearch(os.Stdout, parsed.Format, result); err != nil {
			return err
		}
		if result.ShortRefsRebuilt {
			logInfo(runLog, "short_refs_rebuilt", "alias_index=rebuilt")
		}
		logInfo(runLog, "search_written", fmt.Sprintf("returned=%d total=%d truncated=%t", len(result.Results), result.TotalMatches, result.Truncated))
		return nil
	case "open":
		parsed, err := parseRefCommand("open", args[1:], false)
		if err != nil {
			return err
		}
		if parsed.DBPath != "" {
			paths.Database = parsed.DBPath
		}
		ref, err := resolveInputRef(ctx, paths, parsed.Ref, "open", runLog)
		if err != nil {
			return err
		}
		result, err := archive.Open(ctx, paths, ref)
		if err != nil {
			return err
		}
		if err := writeOpen(os.Stdout, parsed.Format, result); err != nil {
			return err
		}
		logInfo(runLog, "open_written", "ref_kind=asset")
		return nil
	case "neighbors":
		parsed, err := parseRefCommand("neighbors", args[1:], true)
		if err != nil {
			return err
		}
		if parsed.DBPath != "" {
			paths.Database = parsed.DBPath
		}
		ref, err := resolveInputRef(ctx, paths, parsed.Ref, "neighbors", runLog)
		if err != nil {
			return err
		}
		result, err := archive.Neighbors(ctx, paths, archive.NeighborOptions{ID: ref, Limit: parsed.Limit})
		if err != nil {
			return err
		}
		if err := writeNeighbors(os.Stdout, parsed.Format, result); err != nil {
			return err
		}
		logInfo(runLog, "neighbors_written", fmt.Sprintf("returned=%d limit=%d", len(result.Neighbors), result.Limit))
		return nil
	default:
		return usage()
	}
}

func usage() error {
	return output.UsageError{Err: errors.New("usage: photoscrawl <metadata|status|doctor|sync|classify|search|open|neighbors>")}
}

func joinedQuery(flagValue string, args []string) string {
	parts := append([]string{strings.TrimSpace(flagValue)}, args...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func resolveInputRef(ctx context.Context, paths archive.Paths, ref, verb string, runLog interface{ Info(string, string) error }) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") || strings.Contains(ref, "/") {
		return ref, nil
	}
	if !archive.ValidShortRef(ref) {
		return "", commandError{
			Code:    "invalid_ref",
			Message: "ref is not a photoscrawl asset ref",
			Remedy:  "use a ref in the form photoscrawl:asset/ID or a short ref from search",
		}
	}
	resolved, err := archive.ResolveShortRef(ctx, paths, ref)
	if err != nil {
		return "", err
	}
	if resolved.Rebuilt && runLog != nil {
		_ = runLog.Info("short_refs_rebuilt", "alias_index=rebuilt")
	}
	switch len(resolved.FullRefs) {
	case 0:
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found", Remedy: "rerun search or use the full ref"}
	case 1:
		return resolved.FullRefs[0], nil
	default:
		return "", commandError{Code: "ambiguous_short_ref", Message: "short ref matches more than one asset", Remedy: "rerun search or use the full ref"}
	}
}

func doctorFailureCount(checks []archive.DoctorCheck) int {
	var count int
	for _, check := range checks {
		if check.State == "fail" || check.State == "missing" {
			count++
		}
	}
	return count
}
