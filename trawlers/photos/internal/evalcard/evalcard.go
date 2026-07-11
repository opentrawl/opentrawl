package evalcard

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	repoPrompts "github.com/opentrawl/opentrawl/trawlers/photos/prompts"
	crawlconfig "github.com/opentrawl/opentrawl/trawlkit/config"
	ckmodel "github.com/opentrawl/opentrawl/trawlkit/model"
)

const (
	DefaultOllamaGenerateURL = ckmodel.DefaultGenerateURL
)

const PromptVersion = repoPrompts.PhotoCardVersion

var exportOriginalResource = photos.ExportOriginalResourceThroughApp

type originalResolver interface {
	Resolve(context.Context, photos.OriginalRequest) (photos.OriginalResolution, error)
}

type Options struct {
	LibraryPath          string
	OutputDir            string
	CacheDir             string
	DefaultOutputRoot    string
	DefaultCacheDir      string
	PromptPath           string
	Models               []string
	OllamaGenerateURL    string
	OllamaAPIKeyEnv      string
	Limit                int
	Concurrency          int
	Sample               string
	Seed                 uint64
	AllowICloudDownloads bool
	Provider             photos.Provider
	Now                  func() time.Time
}

type Result struct {
	OutputDir             string         `json:"output_dir"`
	CacheDir              string         `json:"cache_dir"`
	PromptPath            string         `json:"prompt_path"`
	PromptSHA256          string         `json:"prompt_sha256"`
	PromptVersion         string         `json:"prompt_version"`
	Models                []string       `json:"models,omitempty"`
	Limit                 int            `json:"limit"`
	Sample                string         `json:"sample"`
	AllowICloudDownloads  bool           `json:"allow_icloud_downloads"`
	AssetsSeen            int            `json:"assets_seen"`
	AssetsPrepared        int            `json:"assets_prepared"`
	AssetsSkipped         map[string]int `json:"assets_skipped,omitempty"`
	ModelCallsAttempted   int            `json:"model_calls_attempted"`
	ModelCallsSucceeded   int            `json:"model_calls_succeeded"`
	ModelCallsFailed      int            `json:"model_calls_failed"`
	ManifestPath          string         `json:"manifest_path"`
	SummaryPath           string         `json:"summary_path"`
	OllamaGenerateURLUsed string         `json:"ollama_generate_url_used,omitempty"`
}

type preparedInput struct {
	ID           string
	ImagePath    string
	MetadataPath string
	MetadataJSON []byte
}

type metadataPack struct {
	EvalID        string         `json:"eval_id"`
	PromptVersion string         `json:"prompt_version"`
	Asset         photos.Asset   `json:"asset"`
	Original      originalSource `json:"original"`
	ImageIO       map[string]any `json:"imageio"`
}

type originalSource struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

type manifestRow struct {
	EvalID       string `json:"eval_id"`
	ImagePath    string `json:"image_path"`
	MetadataPath string `json:"metadata_path"`
	OriginalPath string `json:"original_path"`
	OriginalMode string `json:"original_mode"`
}

func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.Provider == nil {
		return Result{}, errors.New("photos provider is required")
	}
	if strings.TrimSpace(opts.LibraryPath) == "" {
		return Result{}, errors.New("library path is required")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 15
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	sample := strings.TrimSpace(opts.Sample)
	if sample == "" {
		sample = "latest"
	}
	if sample != "latest" && sample != "random" {
		return Result{}, fmt.Errorf("unsupported sample %q", sample)
	}
	ollamaGenerateURLUsed, err := normalizeOllamaGenerateURL(opts.OllamaGenerateURL)
	if err != nil {
		return Result{}, err
	}

	outputDir, err := defaultedOutputDir(opts.OutputDir, opts.DefaultOutputRoot, now)
	if err != nil {
		return Result{}, err
	}
	cacheDir, err := defaultedCacheDir(opts.CacheDir, opts.DefaultCacheDir)
	if err != nil {
		return Result{}, err
	}
	libraryPath, err := filepath.Abs(crawlconfig.ExpandHome(strings.TrimSpace(opts.LibraryPath)))
	if err != nil {
		return Result{}, err
	}
	if err := rejectRepoPath(outputDir); err != nil {
		return Result{}, fmt.Errorf("output dir: %w", err)
	}
	if err := rejectRepoPath(cacheDir); err != nil {
		return Result{}, fmt.Errorf("cache dir: %w", err)
	}
	var resolver originalResolver
	externalDevelopmentCache := strings.TrimSpace(opts.CacheDir) != ""
	if externalDevelopmentCache {
		resolver, err = photos.NewDevelopmentOriginalResolver(cacheDir, libraryPath, exportOriginalResource)
		if err != nil {
			return Result{}, err
		}
	}
	directories := []string{
		outputDir,
		filepath.Join(outputDir, "images"),
		filepath.Join(outputDir, "metadata"),
		filepath.Join(outputDir, "raw"),
	}
	if !externalDevelopmentCache {
		directories = append(directories, cacheDir)
	}
	for _, dir := range directories {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Result{}, err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return Result{}, err
		}
	}

	promptPath := strings.TrimSpace(opts.PromptPath)
	if promptPath == "" {
		promptPath = repoPrompts.DefaultPhotoCardPath
	}
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return Result{}, fmt.Errorf("read prompt: %w", err)
	}
	promptSum := sha256.Sum256(promptBytes)

	snapshot, err := opts.Provider.Snapshot(ctx, libraryPath)
	if err != nil {
		return Result{}, err
	}
	if err := photos.AttachLocalMediaPaths(&snapshot, libraryPath); err != nil {
		return Result{}, fmt.Errorf("resolve local media: %w", err)
	}
	localMedia, err := photos.BuildLocalMediaIndex(libraryPath)
	if err != nil {
		return Result{}, fmt.Errorf("index local media: %w", err)
	}
	if !externalDevelopmentCache {
		resolver, err = photos.NewOriginalResolver(cacheDir, exportOriginalResource)
		if err != nil {
			return Result{}, err
		}
	}
	result := Result{
		OutputDir:             outputDir,
		CacheDir:              cacheDir,
		PromptPath:            promptPath,
		PromptSHA256:          hex.EncodeToString(promptSum[:]),
		PromptVersion:         PromptVersion,
		Models:                append([]string(nil), opts.Models...),
		Limit:                 limit,
		Sample:                sample,
		AllowICloudDownloads:  opts.AllowICloudDownloads,
		AssetsSkipped:         map[string]int{},
		ManifestPath:          filepath.Join(outputDir, "manifest.jsonl"),
		SummaryPath:           filepath.Join(outputDir, "summary.json"),
		OllamaGenerateURLUsed: ollamaGenerateURLUsed,
	}

	assets := imageAssets(snapshot.Assets, sample, opts.Seed)
	result.AssetsSeen = len(assets)
	manifest, err := os.OpenFile(result.ManifestPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = manifest.Close() }()
	writer := bufio.NewWriter(manifest)
	defer func() { _ = writer.Flush() }()

	inputs := []preparedInput{}
	for _, asset := range assets {
		if len(inputs) >= limit {
			break
		}
		id := fmt.Sprintf("E%03d", len(inputs)+1)
		input, row, err := prepareInput(ctx, resolver, localMedia, libraryPath, outputDir, id, asset, opts.AllowICloudDownloads)
		if err != nil {
			result.AssetsSkipped[classifySkip(err)]++
			continue
		}
		rowJSON, err := json.Marshal(row)
		if err != nil {
			return Result{}, err
		}
		if _, err := writer.Write(append(rowJSON, '\n')); err != nil {
			return Result{}, err
		}
		inputs = append(inputs, input)
	}
	result.AssetsPrepared = len(inputs)

	if len(opts.Models) > 0 && len(inputs) > 0 {
		succeeded, failed := runModelCalls(ctx, outputDir, string(promptBytes), inputs, opts.Models, opts.OllamaGenerateURL, opts.OllamaAPIKeyEnv, concurrency)
		result.ModelCallsAttempted = len(inputs) * len(opts.Models)
		result.ModelCallsSucceeded = succeeded
		result.ModelCallsFailed = failed
	}

	summary, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(result.SummaryPath, append(summary, '\n'), 0o600); err != nil {
		return Result{}, err
	}
	return result, nil
}

func prepareInput(ctx context.Context, resolver originalResolver, localMedia photos.LocalMediaIndex, libraryPath, outputDir, id string, asset photos.Asset, allowICloud bool) (preparedInput, manifestRow, error) {
	resolved, err := resolver.Resolve(ctx, originalRequest(localMedia, libraryPath, asset, allowICloud))
	if err != nil {
		return preparedInput{}, manifestRow{}, err
	}
	if resolved.Lease != nil {
		defer resolved.Lease.Close()
	}
	originalPath := resolved.Path
	source := resolved.Source
	imagePath := filepath.Join(outputDir, "images", id+".jpg")
	if err := photos.RenderCanonicalJPEG(ctx, originalPath, imagePath, 0.92); err != nil {
		return preparedInput{}, manifestRow{}, fmt.Errorf("canonical_render")
	}
	if err := os.Chmod(imagePath, 0o600); err != nil {
		return preparedInput{}, manifestRow{}, fmt.Errorf("protect canonical image: %w", err)
	}
	imageMeta, err := photos.ImageMetadata(ctx, originalPath)
	if err != nil {
		return preparedInput{}, manifestRow{}, fmt.Errorf("image_metadata")
	}
	pack := metadataPack{
		EvalID:        id,
		PromptVersion: PromptVersion,
		Asset:         asset,
		Original: originalSource{
			Source: source,
			Path:   originalPath,
		},
		ImageIO: imageMeta,
	}
	metadataJSON, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		return preparedInput{}, manifestRow{}, err
	}
	metadataPath := filepath.Join(outputDir, "metadata", id+".json")
	if err := os.WriteFile(metadataPath, append(metadataJSON, '\n'), 0o600); err != nil {
		return preparedInput{}, manifestRow{}, err
	}
	return preparedInput{
			ID:           id,
			ImagePath:    imagePath,
			MetadataPath: metadataPath,
			MetadataJSON: metadataJSON,
		}, manifestRow{
			EvalID:       id,
			ImagePath:    imagePath,
			MetadataPath: metadataPath,
			OriginalPath: originalPath,
			OriginalMode: source,
		}, nil
}

func imageAssets(assets []photos.Asset, sample string, seed uint64) []photos.Asset {
	out := []photos.Asset{}
	for _, asset := range assets {
		if asset.MediaType == "image" {
			out = append(out, asset)
		}
	}
	if sample == "random" {
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		rng.Shuffle(len(out), func(i, j int) {
			out[i], out[j] = out[j], out[i]
		})
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreationDate != out[j].CreationDate {
			return out[i].CreationDate > out[j].CreationDate
		}
		return out[i].LocalIdentifier < out[j].LocalIdentifier
	})
	return out
}

func originalRequest(localMedia photos.LocalMediaIndex, libraryPath string, asset photos.Asset, allowNetwork bool) photos.OriginalRequest {
	resource, _ := photos.PreferredOriginalResource(asset.Resources)
	return photos.OriginalRequest{
		SourceLibraryID:   photos.SourceLibraryID(libraryPath),
		ModificationDate:  asset.ModificationDate,
		PackageCandidates: localMedia.Candidates(asset.LocalIdentifier),
		AllowNetwork:      allowNetwork,
		Query: photos.OriginalExportQuery{
			LocalIdentifier:  asset.LocalIdentifier,
			CreationDate:     asset.CreationDate,
			Width:            asset.Width,
			Height:           asset.Height,
			OriginalFilename: resource.OriginalFilename,
			OriginalUTI:      resource.UTI,
		},
	}
}

func classifySkip(err error) string {
	value := strings.TrimSpace(err.Error())
	if value == "" {
		return "unknown"
	}
	return value
}
