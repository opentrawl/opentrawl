package archive

import (
	"context"
	"database/sql"
	"testing"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/place"
)

// The place cache can list the same business twice; writing must dedupe
// instead of failing on the derived observation id.
func TestWritePlaceClassificationDedupesIdenticalCandidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	seedSyntheticPlaceAsset(t, paths)

	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		inputs, err := loadClassifyInputs(ctx, tx, 0, "")
		if err != nil {
			return err
		}
		input := inputs[0]
		duplicate := place.POICandidate{
			Name:      "Twice Listed Cafe",
			Category:  "cafe",
			DistanceM: 12,
			Tier:      place.TierVenueCandidate,
			Source:    "fixture",
		}
		input.Place = &classifyPlaceContext{
			CacheStatus: "hit",
			Result: place.Result{
				Provider:      "apple",
				Source:        "fixture",
				RadiusMeters:  150,
				POIStatus:     place.POIStatusFound,
				POICandidates: []place.POICandidate{duplicate, duplicate},
			},
		}
		_, err = writePlaceClassification(ctx, tx, input, venuePlausibility{}, fixedClock("2026-05-28T10:15:00Z")())
		return err
	}); err != nil {
		t.Fatalf("duplicate candidates must dedupe, not fail: %v", err)
	}
}
