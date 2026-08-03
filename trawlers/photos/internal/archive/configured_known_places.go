package archive

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const DefaultConfiguredKnownPlaceRadiusMeters = 75.0

func ListConfiguredKnownPlaces(ctx context.Context, openedStore *store.Store) (*locationwire.KnownPlaceConfiguration, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return nil, err
	}
	rows, err := openedStore.DB().QueryContext(ctx, `
select id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until
from known_place
order by label_kind, display_name
`)
	if err != nil {
		return nil, fmt.Errorf("list configured known places: %w", err)
	}
	defer func() { _ = rows.Close() }()

	configuration := new(locationwire.KnownPlaceConfiguration)
	for rows.Next() {
		var knownPlaceID, storedKind, displayName, validFromText, validUntilText string
		var latitude, longitude, radiusMeters float64
		if err := rows.Scan(&knownPlaceID, &storedKind, &displayName, &latitude, &longitude, &radiusMeters, &validFromText, &validUntilText); err != nil {
			return nil, fmt.Errorf("read configured known place: %w", err)
		}
		kind, err := configuredKnownPlaceKind(storedKind)
		if err != nil {
			return nil, err
		}
		validFrom, err := optionalLocationTimestamp(validFromText)
		if err != nil {
			return nil, fmt.Errorf("read configured known-place start time: %w", err)
		}
		validUntil, err := optionalLocationTimestamp(validUntilText)
		if err != nil {
			return nil, fmt.Errorf("read configured known-place end time: %w", err)
		}
		configuration.Places = append(configuration.Places, &locationwire.ConfiguredKnownPlace{
			KnownPlaceId: knownPlaceID,
			Kind:         kind,
			DisplayName:  displayName,
			Coordinate:   &locationwire.Coordinate{Latitude: latitude, Longitude: longitude},
			RadiusMeters: radiusMeters,
			ValidFrom:    validFrom,
			ValidUntil:   validUntil,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list configured known places: %w", err)
	}
	return configuration, nil
}

func SetConfiguredKnownPlace(ctx context.Context, openedStore *store.Store, requestedPlace *locationwire.ConfiguredKnownPlace) (*locationwire.ConfiguredKnownPlace, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return nil, err
	}
	place, storedKind, err := normalizeConfiguredKnownPlace(requestedPlace)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = openedStore.DB().ExecContext(ctx, `
insert into known_place(id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until, updated_at)
values (?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(label_kind, display_name) do update set
  latitude = excluded.latitude,
  longitude = excluded.longitude,
  radius_meters = excluded.radius_meters,
  valid_from = excluded.valid_from,
  valid_until = excluded.valid_until,
  updated_at = excluded.updated_at
`, place.GetKnownPlaceId(), storedKind, place.GetDisplayName(), place.GetCoordinate().GetLatitude(), place.GetCoordinate().GetLongitude(), place.GetRadiusMeters(), configuredKnownPlaceTimestampText(place.GetValidFrom()), configuredKnownPlaceTimestampText(place.GetValidUntil()), now)
	if err != nil {
		return nil, fmt.Errorf("save configured known place: %w", err)
	}
	return place, nil
}

func RemoveConfiguredKnownPlace(ctx context.Context, openedStore *store.Store, kind locationwire.ConfiguredKnownPlaceKind, displayName string) (bool, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return false, err
	}
	storedKind, err := configuredKnownPlaceKindDatabaseValue(kind)
	if err != nil {
		return false, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return false, errors.New("known-place name cannot be empty")
	}
	result, err := openedStore.DB().ExecContext(ctx, `delete from known_place where label_kind=? and display_name=?`, storedKind, displayName)
	if err != nil {
		return false, fmt.Errorf("remove configured known place: %w", err)
	}
	removedCount, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read configured known-place removal outcome: %w", err)
	}
	return removedCount != 0, nil
}

func normalizeConfiguredKnownPlace(requestedPlace *locationwire.ConfiguredKnownPlace) (*locationwire.ConfiguredKnownPlace, string, error) {
	if requestedPlace == nil {
		return nil, "", errors.New("configured known place is missing")
	}
	storedKind, err := configuredKnownPlaceKindDatabaseValue(requestedPlace.GetKind())
	if err != nil {
		return nil, "", err
	}
	displayName := strings.TrimSpace(requestedPlace.GetDisplayName())
	if displayName == "" {
		return nil, "", errors.New("known-place name cannot be empty")
	}
	coordinate := requestedPlace.GetCoordinate()
	if coordinate == nil || !validConfiguredKnownPlaceCoordinate(coordinate) {
		return nil, "", errors.New("known-place latitude or longitude is invalid")
	}
	radiusMeters := requestedPlace.GetRadiusMeters()
	if radiusMeters == 0 {
		radiusMeters = DefaultConfiguredKnownPlaceRadiusMeters
	}
	if radiusMeters < 0 || math.IsNaN(radiusMeters) || math.IsInf(radiusMeters, 0) {
		return nil, "", errors.New("known-place radius must be greater than zero")
	}
	validFrom, err := validConfiguredKnownPlaceTimestamp(requestedPlace.GetValidFrom())
	if err != nil {
		return nil, "", fmt.Errorf("known-place start time is invalid: %w", err)
	}
	validUntil, err := validConfiguredKnownPlaceTimestamp(requestedPlace.GetValidUntil())
	if err != nil {
		return nil, "", fmt.Errorf("known-place end time is invalid: %w", err)
	}
	if validFrom != nil && validUntil != nil && validUntil.AsTime().Before(validFrom.AsTime()) {
		return nil, "", errors.New("known-place end time cannot be before its start time")
	}
	place := &locationwire.ConfiguredKnownPlace{
		KnownPlaceId: stableID("known_place", storedKind, displayName),
		Kind:         requestedPlace.GetKind(),
		DisplayName:  displayName,
		Coordinate:   proto.Clone(coordinate).(*locationwire.Coordinate),
		RadiusMeters: radiusMeters,
		ValidFrom:    validFrom,
		ValidUntil:   validUntil,
	}
	return place, storedKind, nil
}

func validConfiguredKnownPlaceCoordinate(coordinate *locationwire.Coordinate) bool {
	latitude := coordinate.GetLatitude()
	longitude := coordinate.GetLongitude()
	return !math.IsNaN(latitude) && !math.IsInf(latitude, 0) && latitude >= -90 && latitude <= 90 &&
		!math.IsNaN(longitude) && !math.IsInf(longitude, 0) && longitude >= -180 && longitude <= 180
}

func validConfiguredKnownPlaceTimestamp(value *timestamppb.Timestamp) (*timestamppb.Timestamp, error) {
	if value == nil {
		return nil, nil
	}
	if err := value.CheckValid(); err != nil {
		return nil, err
	}
	return timestamppb.New(value.AsTime()), nil
}

func configuredKnownPlaceTimestampText(value *timestamppb.Timestamp) string {
	if value == nil {
		return ""
	}
	return value.AsTime().UTC().Format(time.RFC3339)
}

func configuredKnownPlaceKindDatabaseValue(kind locationwire.ConfiguredKnownPlaceKind) (string, error) {
	switch kind {
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_HOME:
		return "home", nil
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_FORMER_HOME:
		return "former_home", nil
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_WORK:
		return "work", nil
	default:
		return "", errors.New("known-place type must be home, former-home or work")
	}
}
