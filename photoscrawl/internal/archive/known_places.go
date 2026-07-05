package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/crawlkit/store"
)

const (
	KnownPlaceKindHome       = "home"
	KnownPlaceKindFormerHome = "former_home"
	KnownPlaceKindWork       = "work"

	defaultKnownPlaceRadiusMeters = 75.0
	knownPlaceObservationType     = "known_place"
	knownPlaceSource              = "known_place"
	knownPlaceTier                = "known_place"
)

type KnownPlace struct {
	LabelKind    string  `json:"label_kind"`
	DisplayName  string  `json:"display_name"`
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	RadiusMeters float64 `json:"radius_meters,omitempty"`
	ValidFrom    string  `json:"valid_from,omitempty"`
	ValidUntil   string  `json:"valid_until,omitempty"`
}

type KnownPlaceSetResult struct {
	Database string       `json:"database"`
	Upserted int          `json:"upserted"`
	Places   []KnownPlace `json:"places"`
}

type KnownPlaceListResult struct {
	Database string       `json:"database"`
	Places   []KnownPlace `json:"places"`
}

type KnownPlaceMatch struct {
	Kind           string
	Name           string
	DistanceMeters float64
	// After marks a photo taken after the place's validity window: the spot
	// was the user's home or workplace once, and that relationship still
	// explains the visit better than whichever business is registered nearby.
	After bool
}

func SetKnownPlaces(ctx context.Context, paths Paths, places []KnownPlace) (KnownPlaceSetResult, error) {
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return KnownPlaceSetResult{}, err
	}
	defer func() { _ = db.Close() }()

	normalized := make([]KnownPlace, 0, len(places))
	for i, place := range places {
		row, err := normalizeKnownPlace(place)
		if err != nil {
			return KnownPlaceSetResult{}, fmt.Errorf("known place %d: %w", i+1, err)
		}
		normalized = append(normalized, row)
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, place := range normalized {
			id := stableID("known_place", place.LabelKind, place.DisplayName)
			if _, err := tx.ExecContext(ctx, `
insert into known_place(id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until, updated_at)
values (?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(label_kind, display_name) do update set
  latitude = excluded.latitude,
  longitude = excluded.longitude,
  radius_meters = excluded.radius_meters,
  valid_from = excluded.valid_from,
  valid_until = excluded.valid_until,
  updated_at = excluded.updated_at
`, id, place.LabelKind, place.DisplayName, place.Latitude, place.Longitude, place.RadiusMeters, place.ValidFrom, place.ValidUntil, updatedAt); err != nil {
				return fmt.Errorf("upsert known place: %w", err)
			}
		}
		return nil
	}); err != nil {
		return KnownPlaceSetResult{}, err
	}
	return KnownPlaceSetResult{
		Database: paths.Database,
		Upserted: len(normalized),
		Places:   normalized,
	}, nil
}

func ListKnownPlaces(ctx context.Context, paths Paths) (KnownPlaceListResult, error) {
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return KnownPlaceListResult{}, err
	}
	defer func() { _ = db.Close() }()
	places, err := loadKnownPlaces(ctx, db.DB())
	if err != nil {
		return KnownPlaceListResult{}, err
	}
	return KnownPlaceListResult{Database: paths.Database, Places: places}, nil
}

func normalizeKnownPlace(place KnownPlace) (KnownPlace, error) {
	place.LabelKind = strings.TrimSpace(place.LabelKind)
	place.DisplayName = strings.TrimSpace(place.DisplayName)
	if err := validateKnownPlaceKind(place.LabelKind); err != nil {
		return KnownPlace{}, err
	}
	if place.DisplayName == "" {
		return KnownPlace{}, errors.New("display_name is required")
	}
	if !finiteCoordinate(place.Latitude, place.Longitude) {
		return KnownPlace{}, errors.New("latitude and longitude must be finite and in range")
	}
	if place.Latitude == 0 && place.Longitude == 0 {
		return KnownPlace{}, errors.New("latitude and longitude are required")
	}
	if place.RadiusMeters <= 0 {
		place.RadiusMeters = defaultKnownPlaceRadiusMeters
	}
	if math.IsNaN(place.RadiusMeters) || math.IsInf(place.RadiusMeters, 0) {
		return KnownPlace{}, errors.New("radius_meters must be finite")
	}
	validFrom, fromTime, fromOK, err := normalizeKnownPlaceTime(place.ValidFrom)
	if err != nil {
		return KnownPlace{}, fmt.Errorf("valid_from: %w", err)
	}
	validUntil, untilTime, untilOK, err := normalizeKnownPlaceTime(place.ValidUntil)
	if err != nil {
		return KnownPlace{}, fmt.Errorf("valid_until: %w", err)
	}
	if fromOK && untilOK && untilTime.Before(fromTime) {
		return KnownPlace{}, errors.New("valid_until must not be before valid_from")
	}
	place.ValidFrom = validFrom
	place.ValidUntil = validUntil
	return place, nil
}

func validateKnownPlaceKind(kind string) error {
	switch kind {
	case KnownPlaceKindHome, KnownPlaceKindFormerHome, KnownPlaceKindWork:
		return nil
	default:
		return errors.New("label_kind must be one of home, former_home, work")
	}
}

func finiteCoordinate(lat, lon float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
		return false
	}
	return lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

func normalizeKnownPlaceTime(value string) (string, time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", time.Time{}, false, nil
	}
	parsed, err := flags.Date(value)
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("must be a date (2006-01-02) or RFC 3339 timestamp: %q", value)
	}
	return parsed.Format(time.RFC3339), parsed, true, nil
}

func loadKnownPlaces(ctx context.Context, db *sql.DB) ([]KnownPlace, error) {
	if ok, err := tableExists(ctx, db, "known_place"); err != nil {
		return nil, err
	} else if !ok {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
select label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until
from known_place
order by label_kind, display_name
`)
	if err != nil {
		return nil, fmt.Errorf("load known places: %w", err)
	}
	defer func() { _ = rows.Close() }()
	places := []KnownPlace{}
	for rows.Next() {
		var place KnownPlace
		if err := rows.Scan(&place.LabelKind, &place.DisplayName, &place.Latitude, &place.Longitude, &place.RadiusMeters, &place.ValidFrom, &place.ValidUntil); err != nil {
			return nil, err
		}
		places = append(places, place)
	}
	return places, rows.Err()
}

func matchKnownPlace(places []KnownPlace, latitude, longitude float64, creationDate string) *KnownPlaceMatch {
	if len(places) == 0 || !finiteCoordinate(latitude, longitude) {
		return nil
	}
	takenAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(creationDate))
	if err != nil {
		return nil
	}
	var best *KnownPlaceMatch
	bestRank := 0
	for _, place := range places {
		phase := knownPlaceWindowPhase(place, takenAt)
		if phase < 0 {
			continue
		}
		distance := knownPlaceDistanceMeters(latitude, longitude, place.Latitude, place.Longitude)
		if distance > place.RadiusMeters {
			continue
		}
		after := phase > 0
		rank := knownPlaceKindRank(place.LabelKind)
		betterPhase := best != nil && best.After && !after
		worsePhase := best != nil && !best.After && after
		if best == nil || betterPhase || (!worsePhase && (distance < best.DistanceMeters ||
			(distance == best.DistanceMeters && rank < bestRank) ||
			(distance == best.DistanceMeters && rank == bestRank && place.DisplayName < best.Name))) {
			bestRank = rank
			best = &KnownPlaceMatch{
				Kind:           place.LabelKind,
				Name:           place.DisplayName,
				DistanceMeters: distance,
				After:          after,
			}
		}
	}
	return best
}

func knownPlaceWindowPhase(place KnownPlace, takenAt time.Time) int {
	if from, ok := parseKnownPlaceStoredTime(place.ValidFrom); ok && takenAt.Before(from) {
		return -1
	}
	if until, ok := parseKnownPlaceStoredTime(place.ValidUntil); ok && takenAt.After(until) {
		return 1
	}
	return 0
}

func parseKnownPlaceStoredTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func knownPlaceKindRank(kind string) int {
	switch kind {
	case KnownPlaceKindHome:
		return 0
	case KnownPlaceKindFormerHome:
		return 1
	case KnownPlaceKindWork:
		return 2
	default:
		return 9
	}
}

func knownPlaceDistanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusMeters = 6371008.8
	aLat := degreesToRadians(lat1)
	bLat := degreesToRadians(lat2)
	dLat := degreesToRadians(lat2 - lat1)
	dLon := degreesToRadians(lon2 - lon1)
	sinLat := math.Sin(dLat / 2)
	sinLon := math.Sin(dLon / 2)
	h := sinLat*sinLat + math.Cos(aLat)*math.Cos(bLat)*sinLon*sinLon
	if h > 1 {
		h = 1
	}
	return 2 * earthRadiusMeters * math.Asin(math.Sqrt(h))
}

func degreesToRadians(value float64) float64 {
	return value * math.Pi / 180
}

func KnownPlaceCardLine(kind, name string, after bool) string {
	name = strings.TrimSpace(name)
	if after {
		label := "At former home"
		if kind == KnownPlaceKindWork {
			label = "At former workplace"
		}
		if name == "" {
			return label
		}
		return label + " (" + name + ")"
	}
	switch kind {
	case KnownPlaceKindHome:
		return "At home"
	case KnownPlaceKindFormerHome:
		if name == "" {
			return "At home at the time"
		}
		return "At home at the time (" + name + ")"
	case KnownPlaceKindWork:
		if name == "" {
			return "At work"
		}
		return "At work (" + name + ")"
	default:
		return ""
	}
}

func KnownPlaceWhereLabel(kind, name string, after bool) string {
	name = strings.TrimSpace(name)
	if after {
		label := "former home"
		if kind == KnownPlaceKindWork {
			label = "former workplace"
		}
		if name == "" {
			return label
		}
		return label + " — " + name
	}
	switch kind {
	case KnownPlaceKindHome:
		return "home"
	case KnownPlaceKindFormerHome:
		if name == "" {
			return "home at the time"
		}
		return name + " (home at the time)"
	case KnownPlaceKindWork:
		if name == "" {
			return "work"
		}
		return "work — " + name
	default:
		return ""
	}
}
