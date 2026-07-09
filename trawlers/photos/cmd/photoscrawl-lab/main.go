package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/evalcard"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	"github.com/opentrawl/opentrawl/trawlkit/output"
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

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	paths, err := archive.DefaultPaths()
	if err != nil {
		return err
	}
	switch args[0] {
	case "place-context":
		fs := flag.NewFlagSet("place-context", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		inputPath := fs.String("input", "-", "JSON place input or cached place-context result path, or stdin")
		radius := fs.Float64("radius", 150, "nearby POI search radius in meters")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := place.Run(ctx, place.Options{
			InputPath:    *inputPath,
			RadiusMeters: *radius,
			CacheDir:     paths.PlaceContextCacheDir(),
		})
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "place_context", result)
	case "eval-card":
		fs := flag.NewFlagSet("eval-card", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		libraryPath := fs.String("library", "", "Photos Library.photoslibrary path")
		outDir := fs.String("out", "", "private eval output directory")
		cacheDir := fs.String("cache-dir", "", "private original cache directory")
		promptPath := fs.String("prompt", "", "photo-card prompt file")
		models := fs.String("models", "", "comma-separated model names")
		ollamaURL := fs.String("ollama-url", "", "model generate URL or base URL")
		allowICloud := fs.Bool("allow-icloud-downloads", false, "allow PhotoKit to download missing originals")
		limit := fs.Int("limit", 15, "max images to prepare")
		concurrency := fs.Int("concurrency", 4, "max concurrent model calls")
		sample := fs.String("sample", "latest", "sample mode: latest or random")
		seed := fs.Uint64("seed", 1, "random sample seed")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := evalcard.Run(ctx, evalcard.Options{
			LibraryPath:          *libraryPath,
			OutputDir:            *outDir,
			CacheDir:             *cacheDir,
			DefaultOutputRoot:    paths.EvalRootDir(),
			DefaultCacheDir:      paths.OriginalsCacheDir(),
			PromptPath:           *promptPath,
			Models:               splitList(*models),
			OllamaGenerateURL:    *ollamaURL,
			OllamaAPIKeyEnv:      "OLLAMA_API_KEY",
			Limit:                *limit,
			Concurrency:          *concurrency,
			Sample:               *sample,
			Seed:                 *seed,
			AllowICloudDownloads: *allowICloud,
			Provider:             photos.NewProvider(),
		})
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "eval_card", result)
	case "known-places":
		return runKnownPlaces(ctx, paths, args[1:])
	default:
		return usage()
	}
}

func usage() error {
	return output.UsageError{Err: errors.New("usage: photoscrawl-lab <place-context|eval-card|known-places>")}
}

func runKnownPlaces(ctx context.Context, paths archive.Paths, args []string) error {
	if len(args) == 0 {
		return knownPlacesUsage()
	}
	switch args[0] {
	case "set":
		fs := flag.NewFlagSet("known-places set", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		inputPath := fs.String("input", "", "JSON array of known places")
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if strings.TrimSpace(*inputPath) == "" {
			return output.UsageError{Err: errors.New("known-places set requires --input <json>")}
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		places, err := readKnownPlacesInput(*inputPath)
		if err != nil {
			return err
		}
		result, err := archive.SetKnownPlaces(ctx, paths, places)
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "known_places", result)
	case "list":
		fs := flag.NewFlagSet("known-places list", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		jsonFlag := fs.Bool("json", false, "write JSON")
		formatFlag := fs.String("format", "", "output format")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		format, err := output.Resolve(*formatFlag, *jsonFlag)
		if err != nil {
			return err
		}
		result, err := archive.ListKnownPlaces(ctx, paths)
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, format, "known_places", result)
	default:
		return knownPlacesUsage()
	}
}

func knownPlacesUsage() error {
	return output.UsageError{Err: errors.New("usage: photoscrawl-lab known-places <set|list>")}
}

func readKnownPlacesInput(path string) ([]archive.KnownPlace, error) {
	var reader io.Reader = os.Stdin
	var closeFile func() error
	if strings.TrimSpace(path) != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		reader = file
		closeFile = file.Close
	}
	if closeFile != nil {
		defer func() { _ = closeFile() }()
	}
	var places []archive.KnownPlace
	if err := json.NewDecoder(reader).Decode(&places); err != nil {
		return nil, fmt.Errorf("read known places input: %w", err)
	}
	return places, nil
}

func splitList(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

type commandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Remedy  string `json:"remedy"`
}

func (e commandError) Error() string {
	return e.Message
}

func writeError(w io.Writer, err error) error {
	contractErr := normaliseError(err)
	return json.NewEncoder(w).Encode(map[string]commandError{"error": contractErr})
}

func humanError(err error) string {
	contractErr := normaliseError(err)
	if contractErr.Remedy == "" {
		return contractErr.Message
	}
	return contractErr.Message + ". Remedy: " + contractErr.Remedy
}

func normaliseError(err error) commandError {
	var contractErr commandError
	if errors.As(err, &contractErr) {
		return contractErr
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "command failed"
	}
	switch {
	case output.IsUsage(err):
		return commandError{Code: "usage", Message: message, Remedy: "use photoscrawl-lab <verb> [arguments] [flags]"}
	case strings.Contains(message, "photokit export already running"):
		return commandError{Code: "export_already_running", Message: message, Remedy: "wait for the other eval-card run to finish, then rerun the command"}
	default:
		return commandError{Code: "command_failed", Message: message, Remedy: "fix the reported problem and rerun the command"}
	}
}

func wantsJSON(args []string) bool {
	for i, arg := range args {
		if arg == "--json" || arg == "--format=json" {
			return true
		}
		if arg == "--format" && i+1 < len(args) && args[i+1] == "json" {
			return true
		}
	}
	return false
}
