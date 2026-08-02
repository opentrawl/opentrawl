// Command import-retained-location-evidence is a private, one-off reprojection
// tool. It is intentionally maintained only on a local branch and must not be
// shipped with OpenTrawl.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	currentNearbyPlaceRadiusMetres       = 500
	currentMaximumNearbyPlaceCandidates  = 100
	expectedRetainedAppleOutputs         = 20349
	expectedAppleCoordinateMatches       = 20315
	expectedAppleCoordinateMismatches    = 34
	expectedAppleFloatingPointRoundTrips = 8
	expectedKnownPlaceDefinitions        = 9
	expectedLegacyKnownPlaceObservations = 1151
	expectedDiscardedVenueRows           = 1363
	expectedDiscardedTierJudgements      = 3342
	expectedGeoapifyProofOutcomes        = 12
)

type commandOptions struct {
	legacyArchivePath             string
	legacyAppleOutputRoot         string
	typedLocationProofArchivePath string
	targetArchivePath             string
	applyToTargetArchive          bool
	measureReadinessOnly          bool
	reprojectCurrentArchiveSchema bool
	applySchemaReprojection       bool
	schemaReprojectionBackupPath  string
}

func main() {
	options := parseCommandOptions()
	if err := run(context.Background(), options); err != nil {
		fmt.Fprintf(os.Stderr, "Location evidence import stopped: %v\n", err)
		os.Exit(1)
	}
}

func parseCommandOptions() commandOptions {
	var options commandOptions
	flag.StringVar(&options.legacyArchivePath, "legacy-archive", "", "canonical legacy Photos archive")
	flag.StringVar(&options.legacyAppleOutputRoot, "legacy-apple-output-root", "", "directory containing retained successful Apple adapter outputs")
	flag.StringVar(&options.typedLocationProofArchivePath, "typed-location-proof-archive", "", "current-schema proof archive containing retained typed Geoapify outcomes")
	flag.StringVar(&options.targetArchivePath, "target-archive", "", "current Photos v1 archive")
	flag.BoolVar(&options.applyToTargetArchive, "apply-to-target-archive", false, "write the validated import plan to the target archive")
	flag.BoolVar(&options.measureReadinessOnly, "measure-readiness-only", false, "inspect aggregate backfill readiness without reading retained import sources")
	flag.BoolVar(&options.reprojectCurrentArchiveSchema, "reproject-current-archive-schema", false, "validate the one-off current archive schema reprojection")
	flag.BoolVar(&options.applySchemaReprojection, "apply-current-archive-schema-reprojection", false, "apply the validated one-off current archive schema reprojection")
	flag.StringVar(&options.schemaReprojectionBackupPath, "schema-reprojection-backup", "", "new SQLite backup path required for schema reprojection apply")
	flag.Parse()
	return options
}

type targetCaptureIdentity struct {
	assetID         string
	localIdentifier string
	captureTime     *timestamppb.Timestamp
	coordinate      *locationwire.Coordinate
}

type knownPlaceDefinition struct {
	id           string
	labelKind    string
	displayName  string
	latitude     float64
	longitude    float64
	radiusMetres float64
	validFrom    string
	validUntil   string
	updatedAt    string
}

type importPlan struct {
	knownPlaceDefinitions        []*knownPlaceDefinition
	knownPlaceOutcomes           map[string]*locationwire.MatchConfiguredKnownPlaceOutcome
	appleReverseOutcomes         map[string]*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome
	geoapifyReverse              map[string]*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome
	geoapifyNearby               map[string]*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome
	appleOutputFiles             int
	appleCoordinateMatches       int
	appleCoordinateMismatches    int
	appleFloatingPointRoundTrips int
	appleMissingTargetAssets     int
	legacyKnownPlaceObservations int
	discardedVenueRows           int
	discardedTierJudgements      int
}

func run(ctx context.Context, options commandOptions) error {
	if options.reprojectCurrentArchiveSchema || options.applySchemaReprojection {
		if strings.TrimSpace(options.targetArchivePath) == "" || !filepath.IsAbs(options.targetArchivePath) {
			return errors.New("an absolute target archive path is required for schema reprojection")
		}
		return reprojectCurrentArchiveSchema(ctx, options.targetArchivePath, options.schemaReprojectionBackupPath, options.applySchemaReprojection)
	}
	if options.measureReadinessOnly {
		if strings.TrimSpace(options.targetArchivePath) == "" || !filepath.IsAbs(options.targetArchivePath) {
			return errors.New("an absolute target archive path is required for readiness measurement")
		}
		return measureBackfillReadiness(ctx, options.targetArchivePath)
	}
	if err := validateCommandOptions(options); err != nil {
		return err
	}
	legacyStore, err := store.OpenForeignReadOnly(ctx, options.legacyArchivePath)
	if err != nil {
		return fmt.Errorf("open canonical legacy archive read-only: %w", err)
	}
	defer func() { _ = legacyStore.Close() }()
	targetStore, err := store.OpenReadOnly(ctx, options.targetArchivePath)
	if err != nil {
		return fmt.Errorf("open target Photos archive read-only: %w", err)
	}

	legacyLocalIdentifiersByAssetID, err := loadLegacyLocalIdentifiersByAssetID(ctx, legacyStore.DB())
	if err != nil {
		_ = targetStore.Close()
		return err
	}
	targetCapturesByLocalIdentifier, targetCapturesByAssetID, err := loadTargetCaptureIdentities(ctx, targetStore.DB())
	if err != nil {
		_ = targetStore.Close()
		return err
	}
	knownPlaces, err := loadKnownPlaceDefinitions(ctx, legacyStore.DB())
	if err != nil {
		_ = targetStore.Close()
		return err
	}
	knownOutcomes, err := recomputeKnownPlaceOutcomes(ctx, knownPlaces, targetCapturesByAssetID)
	if err != nil {
		_ = targetStore.Close()
		return err
	}
	if err := reuseStoredKnownPlaceOutcomesWithExactCurrentRequest(ctx, targetStore.DB(), knownOutcomes); err != nil {
		_ = targetStore.Close()
		return err
	}
	appleOutcomes, appleFiles, coordinateMatches, coordinateMismatches, floatingPointRoundTrips, missingTargetAssets, err := reprojectAppleReverseOutcomes(
		options.legacyAppleOutputRoot,
		legacyLocalIdentifiersByAssetID,
		targetCapturesByLocalIdentifier,
	)
	if err != nil {
		_ = targetStore.Close()
		return err
	}
	geoapifyReverse, geoapifyNearby, err := rebindTypedGeoapifyProofOutcomes(
		ctx,
		options.typedLocationProofArchivePath,
		legacyLocalIdentifiersByAssetID,
		targetCapturesByLocalIdentifier,
		knownOutcomes,
	)
	if err != nil {
		_ = targetStore.Close()
		return err
	}
	legacyKnownObservations, discardedVenueRows, discardedTierJudgements, err := countDiscardedLegacyJudgements(ctx, legacyStore.DB())
	if err != nil {
		_ = targetStore.Close()
		return err
	}
	plan := &importPlan{
		knownPlaceDefinitions:        knownPlaces,
		knownPlaceOutcomes:           knownOutcomes,
		appleReverseOutcomes:         appleOutcomes,
		geoapifyReverse:              geoapifyReverse,
		geoapifyNearby:               geoapifyNearby,
		appleOutputFiles:             appleFiles,
		appleCoordinateMatches:       coordinateMatches,
		appleCoordinateMismatches:    coordinateMismatches,
		appleFloatingPointRoundTrips: floatingPointRoundTrips,
		appleMissingTargetAssets:     missingTargetAssets,
		legacyKnownPlaceObservations: legacyKnownObservations,
		discardedVenueRows:           discardedVenueRows,
		discardedTierJudgements:      discardedTierJudgements,
	}
	if err := validateCanonicalImportPlan(plan); err != nil {
		_ = targetStore.Close()
		return err
	}
	writes, err := countRequiredTargetWrites(ctx, targetStore.DB(), plan)
	if err != nil {
		_ = targetStore.Close()
		return err
	}
	if err := targetStore.Close(); err != nil {
		return fmt.Errorf("close target read-only inspection: %w", err)
	}
	printImportSummary(options.applyToTargetArchive, plan, writes)
	if !options.applyToTargetArchive {
		fmt.Println("Target archive: unchanged. Add --apply-to-target-archive only after dry-run approval.")
		return nil
	}
	if err := applyImportPlan(ctx, options.targetArchivePath, plan); err != nil {
		return err
	}
	fmt.Println("Target archive: validated location evidence was committed in one transaction.")
	return nil
}

func validateCommandOptions(options commandOptions) error {
	for name, path := range map[string]string{
		"legacy archive":               options.legacyArchivePath,
		"legacy Apple output root":     options.legacyAppleOutputRoot,
		"typed location proof archive": options.typedLocationProofArchivePath,
		"target archive":               options.targetArchivePath,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%s path is required", name)
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("%s path must be absolute", name)
		}
	}
	if sameFilePath(options.legacyArchivePath, options.targetArchivePath) {
		return errors.New("legacy and target archives must be different files")
	}
	return nil
}

func sameFilePath(first, second string) bool {
	firstAbsolute, firstErr := filepath.Abs(first)
	secondAbsolute, secondErr := filepath.Abs(second)
	return firstErr == nil && secondErr == nil && filepath.Clean(firstAbsolute) == filepath.Clean(secondAbsolute)
}
