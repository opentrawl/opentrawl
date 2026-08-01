package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func LoadCaptureLocationInput(ctx context.Context, openedStore *store.Store, assetID string) (*locationwire.CaptureLocationInput, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return nil, err
	}
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return nil, errors.New("Photos asset identity is required")
	}
	var captureTimeText string
	var latitude, longitude sql.NullFloat64
	err := openedStore.DB().QueryRowContext(ctx, `
select asset.creation_date, location.latitude, location.longitude
from asset
left join location_observation location on location.id = (
  select first_location.id
  from location_observation first_location
  where first_location.asset_id = asset.id
  order by first_location.id
  limit 1
)
where asset.id = ? and asset.source_state = 'current'`, assetID).Scan(&captureTimeText, &latitude, &longitude)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("current Photos asset was not found")
	}
	if err != nil {
		return nil, fmt.Errorf("load Photos capture location: %w", err)
	}
	if !latitude.Valid || !longitude.Valid {
		return nil, errors.New("Photos asset has no capture coordinate")
	}
	captureTime, err := time.Parse(time.RFC3339, captureTimeText)
	if err != nil {
		return nil, fmt.Errorf("parse Photos capture time: %w", err)
	}
	return &locationwire.CaptureLocationInput{
		AssetId:     assetID,
		CaptureTime: timestamppb.New(captureTime),
		Coordinate:  &locationwire.Coordinate{Latitude: latitude.Float64, Longitude: longitude.Float64},
	}, nil
}
