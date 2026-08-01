package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type FactualLocationEvidenceAcquisitionOptions struct {
	AssetID                string
	CaptureLocation        place.Input
	KnownCapturePlaces     []place.KnownCapturePlace
	GeoapifyAPIKeyFilePath string
	NearbyRadiusMeters     float64
	AcquiredAt             time.Time
}

type FactualLocationEvidenceAcquisitionResult struct {
	KnownCapturePlaceMatch *place.KnownCapturePlaceMatch
	Evidence               []place.FactualLocationEvidence
	Briefing               string
}

// AcquireAndStoreFactualLocationEvidence obtains mechanical location facts for
// one capture coordinate. It stores provider evidence but leaves photographed
// place judgement to the card model.
func AcquireAndStoreFactualLocationEvidence(ctx context.Context, openedStore *store.Store, options FactualLocationEvidenceAcquisitionOptions) (FactualLocationEvidenceAcquisitionResult, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return FactualLocationEvidenceAcquisitionResult{}, err
	}
	if err := prepareStore(ctx, openedStore); err != nil {
		return FactualLocationEvidenceAcquisitionResult{}, err
	}
	if strings.TrimSpace(options.AssetID) == "" {
		return FactualLocationEvidenceAcquisitionResult{}, fmt.Errorf("Photos asset ID is required")
	}
	if options.CaptureLocation.AssetID != "" && options.CaptureLocation.AssetID != options.AssetID {
		return FactualLocationEvidenceAcquisitionResult{}, fmt.Errorf("capture location asset ID does not match evidence asset ID")
	}
	options.CaptureLocation.AssetID = options.AssetID
	knownCapturePlaceMatch, err := place.MatchKnownCapturePlace(options.CaptureLocation.Location, options.KnownCapturePlaces)
	if err != nil {
		return FactualLocationEvidenceAcquisitionResult{}, err
	}
	evidence, err := (place.FactualLocationEvidenceAcquirer{
		GeoapifyAPIKeyFilePath: options.GeoapifyAPIKeyFilePath,
		NearbyRadiusMeters:     options.NearbyRadiusMeters,
	}).Acquire(ctx, options.CaptureLocation, knownCapturePlaceMatch)
	if err != nil {
		return FactualLocationEvidenceAcquisitionResult{}, err
	}
	acquiredAt := options.AcquiredAt.UTC()
	if acquiredAt.IsZero() {
		acquiredAt = time.Now().UTC()
	}
	if err := openedStore.WithTx(ctx, func(transaction *sql.Tx) error {
		for _, record := range evidence {
			if err := storeFactualLocationEvidence(ctx, transaction, options.AssetID, record, acquiredAt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return FactualLocationEvidenceAcquisitionResult{}, err
	}
	return FactualLocationEvidenceAcquisitionResult{
		KnownCapturePlaceMatch: knownCapturePlaceMatch,
		Evidence:               evidence,
		Briefing:               place.ComposeFactualLocationBriefing(knownCapturePlaceMatch, evidence),
	}, nil
}

func storeFactualLocationEvidence(ctx context.Context, transaction *sql.Tx, assetID string, record place.FactualLocationEvidence, acquiredAt time.Time) error {
	if strings.TrimSpace(record.ProviderIdentity) == "" || strings.TrimSpace(record.Operation) == "" || len(record.RawResponse) == 0 {
		return fmt.Errorf("factual location evidence is incomplete")
	}
	addressJSON, err := json.Marshal(record.Address)
	if err != nil {
		return fmt.Errorf("marshal factual location address: %w", err)
	}
	evidenceID := stableID("factual_location_evidence", assetID, record.ProviderIdentity, record.Operation)
	if _, err := transaction.ExecContext(ctx, `
insert into factual_location_evidence(id, asset_id, provider_identity, operation, address_json, raw_response, acquired_at)
values (?, ?, ?, ?, ?, ?, ?)
on conflict(asset_id, provider_identity, operation) do update set
  address_json = excluded.address_json,
  raw_response = excluded.raw_response,
  acquired_at = excluded.acquired_at
`, evidenceID, assetID, record.ProviderIdentity, record.Operation, addressJSON, record.RawResponse, acquiredAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("store factual location evidence: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `delete from factual_location_evidence_candidate where evidence_id = ?`, evidenceID); err != nil {
		return fmt.Errorf("clear factual location candidates: %w", err)
	}
	for providerPosition, candidate := range record.Candidates {
		categoriesJSON, err := json.Marshal(candidate.Categories)
		if err != nil {
			return fmt.Errorf("marshal factual location candidate categories: %w", err)
		}
		candidateAddressJSON, err := json.Marshal(candidate.Address)
		if err != nil {
			return fmt.Errorf("marshal factual location candidate address: %w", err)
		}
		var latitude, longitude sql.NullFloat64
		if candidate.Coordinate != nil {
			latitude = sql.NullFloat64{Float64: candidate.Coordinate.Latitude, Valid: true}
			longitude = sql.NullFloat64{Float64: candidate.Coordinate.Longitude, Valid: true}
		}
		if _, err := transaction.ExecContext(ctx, `
insert into factual_location_evidence_candidate(
  evidence_id, provider_position, provider_place_identity, display_name,
  categories_json, latitude, longitude, address_json, distance_meters)
values (?, ?, ?, ?, ?, ?, ?, ?, ?)
`, evidenceID, providerPosition, candidate.ProviderPlaceIdentity, candidate.Name,
			categoriesJSON, latitude, longitude, candidateAddressJSON, candidate.DistanceMeters); err != nil {
			return fmt.Errorf("store factual location candidate: %w", err)
		}
	}
	return nil
}
