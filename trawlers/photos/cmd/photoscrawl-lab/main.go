package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	photoscrawl "github.com/opentrawl/opentrawl/trawlers/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/evalcard"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	"github.com/opentrawl/opentrawl/trawlkit/cache"
	ckconfig "github.com/opentrawl/opentrawl/trawlkit/config"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

const placeEvidenceOperationEnv = "PHOTOS_PLACE_EVIDENCE_OPERATION"

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
	case "place-evidence":
		return runPlaceEvidence(ctx, paths, args[1:])
	case "place-evidence-inventory":
		return runPlaceEvidenceInventory(ctx, paths, args[1:])
	case "place-evidence-campaign":
		return runPlaceEvidenceCampaign(ctx, paths, args[1:])
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
	case "audit-card-input":
		return runAuditCardInput(ctx, paths, args[1:])
	case "approved-card":
		return runApprovedCard(ctx, args[1:])
	case "known-places":
		return runKnownPlaces(ctx, paths, args[1:])
	default:
		return usage()
	}
}

func usage() error {
	return output.UsageError{Err: errors.New("usage: photoscrawl-lab <place-evidence|place-evidence-inventory|place-evidence-campaign|place-context|eval-card|audit-card-input|approved-card|known-places>")}
}

func runApprovedCard(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return output.UsageError{Err: errors.New("usage: photoscrawl-lab approved-card <prepare|send>")}
	}
	fs := flag.NewFlagSet("approved-card "+args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	archivePath := fs.String("archive", "", "Photos archive path")
	cacheDir := fs.String("cache-dir", "", "Photos checked-artifact cache directory")
	sourceLibrary := fs.String("source-library", "", "Photos source library identity")
	assets := stringList{}
	fs.Var(&assets, "asset", "asset identity to prepare; repeat for each asset")
	preparedPath := fs.String("prepared", "", "private prepared protobuf bundle")
	approvedSHA256 := fs.String("approve-manifest-sha256", "", "SHA-256 of the prepared bundle to send")
	outDir := fs.String("out", "", "existing owner-only private output directory")
	modelConfigPath := fs.String("model-config", "", "private model credential configuration")
	if err := fs.Parse(args[1:]); err != nil {
		return output.UsageError{Err: err}
	}
	if err := validateCardInputAuditOutput(*outDir); err != nil {
		return err
	}
	switch args[0] {
	case "prepare":
		config, err := readApprovedCardModelConfig(*modelConfigPath)
		if err != nil {
			return err
		}
		prepared, err := archive.PrepareApprovedCardBundle(ctx, archive.ApprovedCardPrepareOptions{
			ArchivePath: *archivePath, CacheDir: *cacheDir, SourceLibraryID: *sourceLibrary,
			AssetIDs: assets, Model: config.Model, ModelURL: config.BaseURL,
			Purpose: "canary", CallCap: len(assets),
		})
		if err != nil {
			return err
		}
		return writeApprovedCardBundle(*outDir, prepared)
	case "send":
		config, err := readApprovedCardModelConfig(*modelConfigPath)
		if err != nil {
			return err
		}
		prepared, err := os.ReadFile(*preparedPath)
		if err != nil {
			return err
		}
		if err := archive.ValidateApprovedCardSend(prepared, strings.TrimSpace(*approvedSHA256)); err != nil {
			return err
		}
		db, err := archive.OpenApprovedCardArchive(ctx, *archivePath)
		if err != nil {
			return err
		}
		defer db.Close()
		client, err := model.New(model.Config{BaseURL: config.BaseURL, Model: config.Model, BearerKeyEnv: config.BearerKeyEnv})
		if err != nil {
			return err
		}
		return archive.SendApprovedCardBundle(ctx, db, prepared, strings.TrimSpace(*approvedSHA256), time.Now().UTC(), client)
	default:
		return output.UsageError{Err: errors.New("usage: photoscrawl-lab approved-card <prepare|send>")}
	}
}

type approvedCardModelConfig struct {
	BaseURL      string `toml:"base_url"`
	Model        string `toml:"model"`
	BearerKeyEnv string `toml:"bearer_key_env"`
}

func readApprovedCardModelConfig(path string) (approvedCardModelConfig, error) {
	var config approvedCardModelConfig
	if err := ckconfig.LoadTOML(path, &config); err != nil {
		return approvedCardModelConfig{}, fmt.Errorf("load approved-card model config: %w", err)
	}
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.Model) == "" {
		return approvedCardModelConfig{}, errors.New("approved-card model config requires base_url and model")
	}
	return config, nil
}

func writeApprovedCardBundle(outDir string, prepared []byte) error {
	file, err := os.OpenFile(filepath.Join(strings.TrimSpace(outDir), "approved-card.pb"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(prepared); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }

func (values *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("asset identity is required")
	}
	*values = append(*values, value)
	return nil
}

type cardInputAuditSelection struct {
	AssetIDs []string `json:"asset_ids"`
}

func runAuditCardInput(ctx context.Context, paths archive.Paths, args []string) error {
	if len(args) == 0 {
		return output.UsageError{Err: errors.New("usage: photoscrawl-lab audit-card-input <inventory|prepare|inspect>")}
	}
	switch args[0] {
	case "inventory":
		fs := flag.NewFlagSet("audit-card-input inventory", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		archivePath := fs.String("archive", "", "complete Photos archive path")
		sourceLibrary := fs.String("source-library", "", "exact source library ID")
		outDir := fs.String("out", "", "existing owner-only private audit output directory")
		jsonFlag := fs.Bool("json", false, "write JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if !*jsonFlag {
			return output.UsageError{Err: errors.New("audit-card-input inventory requires --json")}
		}
		if err := validateCardInputAuditOutput(*outDir); err != nil {
			return err
		}
		auditArchivePath, cleanup, err := snapshotCardInputAuditArchive(ctx, *archivePath, *outDir)
		if err != nil {
			return err
		}
		defer cleanup()
		inventory, err := archive.ReadCardInputAuditInventory(ctx, archive.CardInputAuditInventoryOptions{ArchivePath: auditArchivePath, SourceLibraryID: strings.TrimSpace(*sourceLibrary)})
		if err != nil {
			return err
		}
		path, err := writeCardInputAuditOutput(*outDir, "inventory", inventory)
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, output.JSON, "card_input_audit_output", map[string]string{"path": path})
	case "prepare":
		fs := flag.NewFlagSet("audit-card-input prepare", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		archivePath := fs.String("archive", "", "canonical Photos archive path")
		sourceLibrary := fs.String("source-library", "", "exact source library ID")
		assetID := fs.String("asset", "", "exact asset identity")
		cacheDir := fs.String("cache", "", "private checked-artifact cache directory")
		outDir := fs.String("out", "", "existing owner-only private audit output directory")
		jsonFlag := fs.Bool("json", false, "write JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if !*jsonFlag {
			return output.UsageError{Err: errors.New("audit-card-input prepare requires --json")}
		}
		if strings.TrimSpace(*assetID) == "" || strings.TrimSpace(*cacheDir) == "" {
			return output.UsageError{Err: errors.New("audit-card-input prepare requires --asset and --cache")}
		}
		if err := validateCardInputAuditOutput(*outDir); err != nil {
			return err
		}
		auditArchivePath, cleanup, err := snapshotCardInputAuditArchive(ctx, *archivePath, *outDir)
		if err != nil {
			return err
		}
		defer cleanup()
		prepared, err := archive.PrepareCardInputAudit(ctx, archive.CardInputAuditPrepareOptions{
			CardInputAuditInventoryOptions: archive.CardInputAuditInventoryOptions{ArchivePath: auditArchivePath, SourceLibraryID: strings.TrimSpace(*sourceLibrary)},
			CacheDir:                       strings.TrimSpace(*cacheDir),
			AssetID:                        strings.TrimSpace(*assetID),
		})
		if err != nil {
			return err
		}
		path, err := writeCardInputAuditOutput(*outDir, "prepare", prepared)
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, output.JSON, "card_input_audit_output", map[string]string{"path": path})
	case "inspect":
		fs := flag.NewFlagSet("audit-card-input inspect", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		archivePath := fs.String("archive", "", "complete Photos archive path")
		sourceLibrary := fs.String("source-library", "", "exact source library ID")
		cacheDir := fs.String("cache", "", "private checked-artifact cache directory")
		outDir := fs.String("out", "", "existing owner-only private audit output directory")
		selectionPath := fs.String("selection", "", "private JSON selection with asset_ids")
		backstop := fs.Int("backstop", 0, "number of stable SHA-256 inventory backstop assets")
		modelName := fs.String("model", "", "candidate model name used only to render the request")
		modelURL := fs.String("model-url", "", "candidate model base URL used only to render the request")
		jsonFlag := fs.Bool("json", false, "write JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return output.UsageError{Err: err}
		}
		if !*jsonFlag {
			return output.UsageError{Err: errors.New("audit-card-input inspect requires --json")}
		}
		if strings.TrimSpace(*cacheDir) == "" {
			return output.UsageError{Err: errors.New("audit-card-input inspect requires --cache")}
		}
		if err := validateCardInputAuditOutput(*outDir); err != nil {
			return err
		}
		auditArchivePath, cleanup, err := snapshotCardInputAuditArchive(ctx, *archivePath, *outDir)
		if err != nil {
			return err
		}
		defer cleanup()
		selection, err := readCardInputAuditSelection(*selectionPath)
		if err != nil {
			return err
		}
		inventory, err := archive.ReadCardInputAuditInventory(ctx, archive.CardInputAuditInventoryOptions{ArchivePath: auditArchivePath, SourceLibraryID: strings.TrimSpace(*sourceLibrary)})
		if err != nil {
			return err
		}
		assetIDs := append([]string(nil), selection.AssetIDs...)
		if *backstop > 0 {
			candidates := make([]string, 0, len(inventory.Assets))
			for _, row := range inventory.Assets {
				candidates = append(candidates, row.AssetID)
			}
			assetIDs = append(assetIDs, archive.StableCardInputAuditBackstop(inventory.SnapshotID, candidates, *backstop)...)
		}
		inspections, err := archive.InspectCardInputs(ctx, archive.CardInputAuditInspectOptions{CardInputAuditInventoryOptions: archive.CardInputAuditInventoryOptions{ArchivePath: auditArchivePath, SourceLibraryID: strings.TrimSpace(*sourceLibrary)}, CacheDir: strings.TrimSpace(*cacheDir), AssetIDs: assetIDs, Model: strings.TrimSpace(*modelName), ModelURL: strings.TrimSpace(*modelURL)})
		if err != nil {
			return err
		}
		path, err := writeCardInputAuditOutput(*outDir, "inspect", inspections)
		if err != nil {
			return err
		}
		return output.Write(os.Stdout, output.JSON, "card_input_audit_output", map[string]string{"path": path})
	default:
		return output.UsageError{Err: errors.New("usage: photoscrawl-lab audit-card-input <inventory|prepare|inspect>")}
	}
}

func validateCardInputAuditOutput(path string) error {
	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil {
		return fmt.Errorf("audit card-input output directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("audit card-input output directory must be an existing owner-only directory")
	}
	return nil
}

func snapshotCardInputAuditArchive(ctx context.Context, sourcePath, outDir string) (string, func(), error) {
	root, err := os.MkdirTemp(strings.TrimSpace(outDir), ".archive-snapshot-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create private audit archive snapshot: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	snapshot, err := cache.SnapshotSQLite(ctx, cache.SQLiteSnapshotOptions{
		SourcePath:     strings.TrimSpace(sourcePath),
		DestinationDir: root,
		Name:           "archive.sqlite",
	})
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("snapshot audit card-input archive: %w", err)
	}
	return snapshot.Path, cleanup, nil
}

func writeCardInputAuditOutput(outDir, name string, value any) (string, error) {
	path := filepath.Join(strings.TrimSpace(outDir), name+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create private audit output: %w", err)
	}
	if err := output.Write(file, output.JSON, "card_input_audit_"+name, value); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func readCardInputAuditSelection(path string) (cardInputAuditSelection, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return cardInputAuditSelection{}, errors.New("audit card-input inspect requires --selection")
	}
	file, err := os.Open(path)
	if err != nil {
		return cardInputAuditSelection{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var selection cardInputAuditSelection
	if err := decoder.Decode(&selection); err != nil {
		return cardInputAuditSelection{}, fmt.Errorf("decode audit card-input selection: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return cardInputAuditSelection{}, errors.New("audit card-input selection must contain one JSON object")
	}
	if len(selection.AssetIDs) == 0 {
		return cardInputAuditSelection{}, errors.New("audit card-input selection has no asset_ids")
	}
	return selection, nil
}

func runPlaceEvidenceInventory(ctx context.Context, paths archive.Paths, args []string) (runErr error) {
	fs := flag.NewFlagSet("place-evidence-inventory", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	archivePath := fs.String("archive", "", "schema-13 Photos archive path")
	sourceLibrary := fs.String("source-library", "", "exact source library ID")
	outDir := fs.String("out", "", "existing owner-only private output root")
	jsonFlag := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(args); err != nil {
		return output.UsageError{Err: err}
	}
	if !*jsonFlag {
		return output.UsageError{Err: errors.New("place-evidence-inventory requires --json")}
	}
	if err := place.ValidatePrivateEvidenceInputFile(strings.TrimSpace(*archivePath)); err != nil {
		return err
	}
	logRun, err := newPlaceEvidenceLog(paths, "place-evidence-inventory")
	if err != nil {
		return err
	}
	defer func() {
		if finishErr := logRun.Finish(runErr); runErr == nil {
			runErr = finishErr
		}
	}()
	config, err := loadPhotosLabConfig(paths.ConfigPath)
	if err != nil {
		return err
	}
	configured, err := config.ConfiguredGeoapifyEvidence()
	if err != nil {
		return err
	}
	source := place.EvidenceInventorySource{SourceLibraryID: strings.TrimSpace(*sourceLibrary)}
	inventory, inventoryErr := archive.ReadPlaceEvidenceInventory(ctx, strings.TrimSpace(*archivePath), source.SourceLibraryID)
	if inventoryErr != nil {
		var incomplete *archive.PlaceEvidenceSnapshotIncompleteError
		if errors.As(inventoryErr, &incomplete) {
			source.StopReason = place.EvidenceInventoryStopSnapshotIncomplete
		} else {
			source.StopReason = place.EvidenceInventoryStopUnsafe
		}
	} else {
		source.Snapshot = place.EvidenceSnapshotReceipt{
			ID:                       inventory.Snapshot.ID,
			CompletedAt:              inventory.Snapshot.CompletedAt,
			CompletenessState:        inventory.Snapshot.CompletenessState,
			CompletenessEvidenceJSON: inventory.Snapshot.CompletenessEvidenceJSON,
		}
		for _, asset := range inventory.Assets {
			row := place.EvidenceInventorySourceAsset{AssetID: asset.AssetID, TakenAt: asset.CreationDate}
			if asset.Coordinate != nil {
				row.Location = &place.Coordinate{Latitude: asset.Coordinate.Latitude, Longitude: asset.Coordinate.Longitude}
			}
			source.Assets = append(source.Assets, row)
		}
	}
	summary, err := place.RunEvidenceInventory(ctx, place.EvidenceInventoryOptions{
		Source:    source,
		OutputDir: strings.TrimSpace(*outDir),
		Geoapify:  configuredGeoapify(configured, "", nil),
		LogSink:   logRun,
	})
	if err != nil {
		return err
	}
	return output.Write(os.Stdout, output.JSON, "place_evidence_inventory", summary)
}

func runPlaceEvidenceCampaign(ctx context.Context, paths archive.Paths, args []string) (runErr error) {
	fs := flag.NewFlagSet("place-evidence-campaign", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	manifestPath := fs.String("manifest", "", "private inventory manifest path")
	targetsPath := fs.String("targets", "", "private operator-selected targets path")
	inspectionReceiptPath := fs.String("inspection-receipt", "", "private canary inspection receipt path")
	outDir := fs.String("out", "", "existing owner-only private output root")
	resume := fs.Bool("resume", false, "resume the next incomplete manifest stage")
	jsonFlag := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(args); err != nil {
		return output.UsageError{Err: err}
	}
	if !*jsonFlag {
		return output.UsageError{Err: errors.New("place-evidence-campaign requires --json")}
	}
	logRun, err := newPlaceEvidenceLog(paths, "place-evidence-campaign")
	if err != nil {
		return err
	}
	defer func() {
		if finishErr := logRun.Finish(runErr); runErr == nil {
			runErr = finishErr
		}
	}()
	config, err := loadPhotosLabConfig(paths.ConfigPath)
	if err != nil {
		return err
	}
	configured, err := config.ConfiguredGeoapifyEvidence()
	if err != nil {
		return err
	}
	summary, err := place.RunEvidenceCampaign(ctx, place.EvidenceCampaignOptions{
		ManifestPath:          strings.TrimSpace(*manifestPath),
		TargetsPath:           strings.TrimSpace(*targetsPath),
		InspectionReceiptPath: strings.TrimSpace(*inspectionReceiptPath),
		OutputDir:             strings.TrimSpace(*outDir),
		CacheDir:              filepath.Join(paths.CacheDir, "place-evidence-canary"),
		Resume:                *resume,
		Geoapify:              configuredGeoapify(configured, "", nil),
		LogSink:               logRun,
	})
	if err != nil {
		return err
	}
	return output.Write(os.Stdout, output.JSON, "place_evidence_campaign", summary)
}

func newPlaceEvidenceLog(paths archive.Paths, command string) (*cklog.Run, error) {
	return cklog.NewRun(cklog.Options{StateRoot: filepath.Dir(paths.DataDir), CrawlerID: "photos", Command: command, Version: "dev", Platform: runtime.GOOS + "/" + runtime.GOARCH})
}

func configuredGeoapify(config photoscrawl.GeoapifyEvidenceConfig, credential string, client *http.Client) place.ConfiguredGeoapifyEvidence {
	return place.ConfiguredGeoapifyEvidence{
		ProviderIdentity:    config.ProviderIdentity,
		ReverseEndpoint:     config.ReverseEndpoint,
		NearbyEndpoint:      config.NearbyEndpoint,
		CredentialReference: config.CredentialEnv,
		CredentialParameter: config.CredentialParameter,
		Credential:          credential,
		NearbyCategories:    append([]string(nil), config.NearbyCategories...),
		ReverseLimit:        config.ReverseLimit,
		NearbyLimit:         config.NearbyLimit,
		HTTPClient:          client,
	}
}

func runPlaceEvidence(ctx context.Context, paths archive.Paths, args []string) error {
	return runPlaceEvidenceWith(ctx, paths, args, os.Stdout, place.RunEvidence)
}

func runPlaceEvidenceWith(ctx context.Context, paths archive.Paths, args []string, writer io.Writer, runner func(context.Context, place.EvidenceOptions) (place.EvidenceResult, error)) error {
	fs := flag.NewFlagSet("place-evidence", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	inputPath := fs.String("input", "-", "JSON place input path, or stdin")
	coordinateVariant := fs.String("coordinate-variant", "", "explicit coordinate provenance label")
	radius := fs.Float64("radius", 0, "exact nearby search radius in metres")
	outputDir := fs.String("out", "", "private evidence output directory")
	jsonFlag := fs.Bool("json", false, "write JSON")
	formatFlag := fs.String("format", "", "output format")
	if err := fs.Parse(args); err != nil {
		return output.UsageError{Err: err}
	}
	format, err := output.Resolve(*formatFlag, *jsonFlag)
	if err != nil {
		return err
	}
	operation, err := place.ParseEvidenceOperation(os.Getenv(placeEvidenceOperationEnv))
	if err != nil {
		return err
	}
	config, err := loadPhotosLabConfig(paths.ConfigPath)
	if err != nil {
		return err
	}
	configured, err := config.ConfiguredGeoapifyEvidence()
	if err != nil {
		return err
	}
	credential := strings.TrimSpace(os.Getenv(configured.CredentialEnv))
	if credential == "" {
		return fmt.Errorf("configured place evidence credential %s is unavailable", configured.CredentialEnv)
	}
	input, err := place.LoadEvidenceInput(*inputPath)
	if err != nil {
		return err
	}
	out := strings.TrimSpace(*outputDir)
	if out == "" {
		out = filepath.Join(paths.EvalRootDir(), "place-evidence", time.Now().UTC().Format("20060102T150405.000000000Z"))
	}
	result, err := runner(ctx, place.EvidenceOptions{
		Input:             input,
		CoordinateVariant: *coordinateVariant,
		RadiusMeters:      *radius,
		OutputDir:         out,
		CacheDir:          filepath.Join(paths.CacheDir, "place-evidence-canary"),
		Operation:         operation,
		Geoapify: place.ConfiguredGeoapifyEvidence{
			ProviderIdentity:    configured.ProviderIdentity,
			ReverseEndpoint:     configured.ReverseEndpoint,
			NearbyEndpoint:      configured.NearbyEndpoint,
			CredentialReference: configured.CredentialEnv,
			CredentialParameter: configured.CredentialParameter,
			Credential:          credential,
			NearbyCategories:    append([]string(nil), configured.NearbyCategories...),
			ReverseLimit:        configured.ReverseLimit,
			NearbyLimit:         configured.NearbyLimit,
			HTTPClient: &http.Client{
				Timeout: 30 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				},
			},
		},
	})
	if err != nil {
		return err
	}
	return output.Write(writer, format, "place_evidence", result)
}

func loadPhotosLabConfig(path string) (photoscrawl.Config, error) {
	var config photoscrawl.Config
	if err := ckconfig.LoadTOML(path, &config); err != nil {
		return photoscrawl.Config{}, fmt.Errorf("load Photos config: %w", err)
	}
	if err := config.Validate(); err != nil {
		return photoscrawl.Config{}, err
	}
	return config, nil
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
