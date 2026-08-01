// Command photoscrawl-location-inspect is a private development controller for
// exercising the production Photos location operations against one archived
// asset. It does not access PhotoKit or own a second product workflow.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

const (
	nearbyPlaceRadiusMeters = 150
	maximumNearbyCandidates = 100
)

type commandConfiguration struct {
	privateArchivePath string
	photosAssetID      string
	privateOutputDir   string
	geoapifyKeyPath    string
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	configuration, err := parseCommandConfiguration(arguments, stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stderr, err)
		}
		return 2
	}
	if err := inspectLocationEvidence(ctx, configuration, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func parseCommandConfiguration(arguments []string, stderr io.Writer) (commandConfiguration, error) {
	flags := flag.NewFlagSet("photoscrawl-location-inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	privateArchivePath := flags.String("private-archive", "", "private OpenTrawl Photos archive path")
	photosAssetID := flags.String("asset-id", "", "one private Photos asset identity")
	privateOutputDir := flags.String("output-dir", "", "new private directory for retained Protobuf outcomes")
	geoapifyKeyPath := flags.String("geoapify-key-path", "", "path to a file containing the Geoapify API key")
	if err := flags.Parse(arguments); err != nil {
		return commandConfiguration{}, err
	}
	if flags.NArg() != 0 {
		return commandConfiguration{}, errors.New("photoscrawl-location-inspect does not accept positional arguments")
	}
	configuration := commandConfiguration{
		privateArchivePath: strings.TrimSpace(*privateArchivePath),
		photosAssetID:      strings.TrimSpace(*photosAssetID),
		privateOutputDir:   strings.TrimSpace(*privateOutputDir),
		geoapifyKeyPath:    strings.TrimSpace(*geoapifyKeyPath),
	}
	if configuration.privateArchivePath == "" || configuration.photosAssetID == "" || configuration.privateOutputDir == "" || configuration.geoapifyKeyPath == "" {
		return commandConfiguration{}, errors.New("--private-archive, --asset-id, --output-dir and --geoapify-key-path are required")
	}
	return configuration, nil
}

func inspectLocationEvidence(ctx context.Context, configuration commandConfiguration, stdout io.Writer) error {
	if err := os.Mkdir(configuration.privateOutputDir, 0o700); err != nil {
		return fmt.Errorf("create new private output directory: %w", err)
	}
	openedArchive, err := store.OpenReadOnlyWithSharedTrawlerArchiveFileSetLock(ctx, configuration.privateArchivePath)
	if err != nil {
		return err
	}
	defer func() { _ = openedArchive.Close() }()

	captureLocationInput, err := archive.LoadCaptureLocationInput(ctx, openedArchive, configuration.photosAssetID)
	if err != nil {
		return err
	}
	knownPlaceOutcome, err := archive.MatchConfiguredKnownPlace(ctx, openedArchive, &locationwire.MatchConfiguredKnownPlaceRequest{Input: captureLocationInput})
	if err != nil {
		return err
	}
	if err := writePrivateProtobuf(filepath.Join(configuration.privateOutputDir, "01-known-place.pb"), knownPlaceOutcome); err != nil {
		return err
	}
	appleReverseOutcome, err := place.AcquireAppleReverseGeocodingEvidence(ctx, &locationwire.AcquireAppleReverseGeocodingEvidenceRequest{Input: captureLocationInput})
	if err != nil {
		return err
	}
	if err := writePrivateProtobuf(filepath.Join(configuration.privateOutputDir, "02-apple-reverse.pb"), appleReverseOutcome); err != nil {
		return err
	}
	appleNearbyOutcome, err := place.AcquireAppleNearbyPlaceEvidence(ctx, &locationwire.AcquireAppleNearbyPlaceEvidenceRequest{
		Input: captureLocationInput, RadiusMeters: nearbyPlaceRadiusMeters, MaximumCandidates: maximumNearbyCandidates, KnownPlaceOutcome: knownPlaceOutcome,
	})
	if err != nil {
		return err
	}
	if err := writePrivateProtobuf(filepath.Join(configuration.privateOutputDir, "03-apple-nearby.pb"), appleNearbyOutcome); err != nil {
		return err
	}
	geoapifyReverseOutcome, err := place.AcquireGeoapifyReverseGeocodingEvidence(ctx, &locationwire.AcquireGeoapifyReverseGeocodingEvidenceRequest{Input: captureLocationInput}, configuration.geoapifyKeyPath, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	if err := writePrivateProtobuf(filepath.Join(configuration.privateOutputDir, "04-geoapify-reverse.pb"), geoapifyReverseOutcome); err != nil {
		return err
	}
	geoapifyNearbyOutcome, err := place.AcquireGeoapifyNearbyPlaceEvidence(ctx, &locationwire.AcquireGeoapifyNearbyPlaceEvidenceRequest{
		Input: captureLocationInput, RadiusMeters: nearbyPlaceRadiusMeters, MaximumCandidates: maximumNearbyCandidates, KnownPlaceOutcome: knownPlaceOutcome,
	}, configuration.geoapifyKeyPath, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	if err := writePrivateProtobuf(filepath.Join(configuration.privateOutputDir, "05-geoapify-nearby.pb"), geoapifyNearbyOutcome); err != nil {
		return err
	}
	composedLocationEvidence, err := place.ComposePhotoLocationEvidence(knownPlaceOutcome, appleReverseOutcome, appleNearbyOutcome, geoapifyReverseOutcome, geoapifyNearbyOutcome)
	if err != nil {
		return err
	}
	if err := writePrivateProtobuf(filepath.Join(configuration.privateOutputDir, "06-composed-location-evidence.pb"), composedLocationEvidence); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "Known place: %s; matches %d\n", operationState(knownPlaceOutcome.GetState()), len(knownPlaceOutcome.GetMatches()))
	_, _ = fmt.Fprintf(stdout, "Apple reverse: %s; hierarchy fields %d\n", providerExchangeState(appleReverseOutcome.GetExchange()), populatedAddressFieldCount(appleReverseOutcome.GetAddress()))
	_, _ = fmt.Fprintf(stdout, "Apple nearby: %s; candidates %d\n", providerExchangeState(appleNearbyOutcome.GetExchange()), len(appleNearbyOutcome.GetCandidates()))
	_, _ = fmt.Fprintf(stdout, "Geoapify reverse: %s; hierarchy fields %d\n", providerExchangeState(geoapifyReverseOutcome.GetExchange()), populatedAddressFieldCount(geoapifyReverseOutcome.GetAddress()))
	_, _ = fmt.Fprintf(stdout, "Geoapify nearby: %s; candidates %d\n", providerExchangeState(geoapifyNearbyOutcome.GetExchange()), len(geoapifyNearbyOutcome.GetCandidates()))
	_, _ = fmt.Fprintf(stdout, "Composed evidence: %s; nearby suppressed %t; exposed Apple candidates %d; exposed Geoapify candidates %d\n", operationState(composedLocationEvidence.GetState()), composedLocationEvidence.GetNearbySuppressedForKnownPlace(), len(composedLocationEvidence.GetAppleNearbyCandidates()), len(composedLocationEvidence.GetGeoapifyNearbyCandidates()))
	_, _ = fmt.Fprintln(stdout, "Retained six private typed outcomes.")
	return nil
}

func providerExchangeState(exchange *locationwire.ProviderExchange) string {
	if exchange == nil {
		return "missing"
	}
	result := operationState(exchange.GetState())
	if exchange.GetHttpStatus() != 0 {
		result = fmt.Sprintf("%s; HTTP %d", result, exchange.GetHttpStatus())
	}
	if exchange.GetFailure() != nil {
		result = fmt.Sprintf("%s; %s", result, strings.ToLower(strings.TrimPrefix(exchange.GetFailure().GetClass().String(), "OPERATION_FAILURE_CLASS_")))
	}
	return result
}

func writePrivateProtobuf(path string, message proto.Message) error {
	encoded, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode private location outcome: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create private location outcome: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return fmt.Errorf("write private location outcome: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close private location outcome: %w", err)
	}
	return nil
}

func operationState(state locationwire.OperationState) string {
	switch state {
	case locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED:
		return "request retained"
	case locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED:
		return "transmission started"
	case locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED:
		return "response retained"
	case locationwire.OperationState_OPERATION_STATE_SUCCEEDED:
		return "succeeded"
	case locationwire.OperationState_OPERATION_STATE_NO_RESULT:
		return "no result"
	case locationwire.OperationState_OPERATION_STATE_FAILED:
		return "failed"
	case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
		return "skipped for known place"
	default:
		return "unspecified"
	}
}

func populatedAddressFieldCount(address *locationwire.AddressHierarchy) int {
	if address == nil {
		return 0
	}
	count := len(address.GetAreas())
	for _, value := range []string{
		address.GetName(), address.GetHouseNumber(), address.GetStreet(), address.GetNeighbourhood(), address.GetDistrict(), address.GetCity(),
		address.GetCounty(), address.GetRegion(), address.GetPostcode(), address.GetCountry(), address.GetCountryCode(), address.GetTimeZone(), address.GetFormatted(),
	} {
		if strings.TrimSpace(value) != "" {
			count++
		}
	}
	return count
}
