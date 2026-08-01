package photos

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type CurrentStillRequest struct {
	Freshness    CurrentStillFreshness
	AllowNetwork bool
}

type CurrentStillModification struct {
	UnixSeconds  int64
	Microseconds int32
}

func ParseCurrentStillModification(value string) (CurrentStillModification, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return CurrentStillModification{}, fmt.Errorf("parse current-still modification date: %w", err)
	}
	seconds := parsed.Unix()
	microseconds := int32(parsed.Nanosecond() / int(time.Microsecond))
	modification := CurrentStillModification{UnixSeconds: seconds, Microseconds: microseconds}
	if !modification.valid() {
		return CurrentStillModification{}, errors.New("current-still modification date is outside the supported range")
	}
	return modification, nil
}

func (modification CurrentStillModification) valid() bool {
	return modification.UnixSeconds != 0 && modification.Microseconds >= 0 && modification.Microseconds < int32(time.Second/time.Microsecond)
}

// CurrentStillFreshness contains the one live Photos fact the installed app
// can validate before it returns the user's current rendered image.
type CurrentStillFreshness struct {
	modification CurrentStillModification
}

func CurrentStillFreshnessForModification(modification CurrentStillModification) (CurrentStillFreshness, error) {
	if !modification.valid() {
		return CurrentStillFreshness{}, errors.New("current-still modification instant is invalid")
	}
	return CurrentStillFreshness{modification: modification}, nil
}

func (freshness CurrentStillFreshness) ExpectedModification() (CurrentStillModification, bool) {
	return freshness.modification, freshness.modification.valid()
}

type CurrentStillFact struct {
	MediaType     string `json:"media_type"`
	Orientation   int32  `json:"orientation"`
	PixelWidth    int64  `json:"pixel_width"`
	PixelHeight   int64  `json:"pixel_height"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	PhotoKitCalls int    `json:"photokit_calls"`
}

func (fact CurrentStillFact) Valid() bool {
	return fact.MediaType != "" && fact.Orientation >= 1 && fact.Orientation <= 8 &&
		fact.PixelWidth > 0 && fact.PixelHeight > 0 && fact.Size > 0 &&
		len(fact.SHA256) == 64
}
