package photos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	CurrentStillSourceCache    = "cached_photokit_current_still"
	CurrentStillSourcePhotoKit = "photokit_current_still"
)

type CurrentStillRequest struct {
	SourceLibraryID string
	AssetUUID       string
	Freshness       CurrentStillFreshness
	AllowNetwork    bool
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
	microseconds := int32((parsed.Nanosecond() + 500) / 1000)
	if microseconds == 1_000_000 {
		seconds++
		microseconds = 0
	}
	modification := CurrentStillModification{UnixSeconds: seconds, Microseconds: microseconds}
	if !modification.valid() {
		return CurrentStillModification{}, errors.New("current-still modification date is outside the supported range")
	}
	return modification, nil
}

func (m CurrentStillModification) valid() bool {
	return m.UnixSeconds > 0 && m.Microseconds >= 0 && m.Microseconds < 1_000_000
}

// CurrentStillFreshness is one source fact that versions a current still.
// Constructors make modification time and snapshot fingerprint exclusive.
type CurrentStillFreshness struct {
	modification      *CurrentStillModification
	sourceFingerprint string
}

func CurrentStillFreshnessForModification(modification CurrentStillModification) (CurrentStillFreshness, error) {
	if !modification.valid() {
		return CurrentStillFreshness{}, errors.New("current-still modification instant is invalid")
	}
	return CurrentStillFreshness{modification: &modification}, nil
}

func CurrentStillFreshnessForSourceFingerprint(fingerprint string) (CurrentStillFreshness, error) {
	if !validCurrentStillSourceFingerprint(fingerprint) {
		return CurrentStillFreshness{}, errors.New("current-still source fingerprint must be a lower-case SHA-256")
	}
	return CurrentStillFreshness{sourceFingerprint: fingerprint}, nil
}

func validCurrentStillSourceFingerprint(fingerprint string) bool {
	if len(fingerprint) != sha256.Size*2 {
		return false
	}
	for _, character := range fingerprint {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (f CurrentStillFreshness) ExpectedModification() (CurrentStillModification, bool) {
	if f.modification != nil && f.sourceFingerprint == "" && f.modification.valid() {
		return *f.modification, true
	}
	return CurrentStillModification{}, false
}

func (f CurrentStillFreshness) SourceFingerprint() (string, bool) {
	if f.modification == nil && validCurrentStillSourceFingerprint(f.sourceFingerprint) {
		return f.sourceFingerprint, true
	}
	return "", false
}

func (f CurrentStillFreshness) valid() bool {
	if modification, ok := f.ExpectedModification(); ok {
		return modification.valid()
	}
	return f.modification == nil && validCurrentStillSourceFingerprint(f.sourceFingerprint)
}

type CurrentStillFact struct {
	MediaType     string
	Orientation   int32
	PixelWidth    int64
	PixelHeight   int64
	Size          int64
	SHA256        string
	Timings       CurrentStillPhaseTimings
	PhotoKitCalls int
}

// CurrentStillPhaseTimings records the non-overlapping acquisition phases for
// one current-still observation. Microseconds preserve short cache phases
// without implying network bandwidth.
type CurrentStillPhaseTimings struct {
	QueueWaitMicros           int64 `json:"queue_wait_micros"`
	HelperVerificationMicros  int64 `json:"helper_verification_micros"`
	LaunchServicesStartMicros int64 `json:"launch_services_start_micros"`
	PhotoKitCallbackMicros    int64 `json:"photokit_callback_micros"`
	ValidationHashMicros      int64 `json:"validation_hash_micros"`
	CacheInstallationMicros   int64 `json:"cache_installation_micros"`
	TotalMicros               int64 `json:"total_micros"`
}

// CurrentStillMeasuredError keeps timings for failed observations while
// preserving the typed cause for errors.Is and errors.As.
type CurrentStillMeasuredError struct {
	Cause         error
	Timings       CurrentStillPhaseTimings
	PhotoKitCalls int
}

func (e *CurrentStillMeasuredError) Error() string { return e.Cause.Error() }

func (e *CurrentStillMeasuredError) Unwrap() error { return e.Cause }

type CurrentStillResolution struct {
	Path          string
	Source        string
	Fact          CurrentStillFact
	Exported      bool
	Timings       CurrentStillPhaseTimings
	PhotoKitCalls int
	Lease         *OriginalLease
}

type CurrentStillExporter func(context.Context, CurrentStillRequest, string) (CurrentStillFact, error)

type CurrentStillResolver struct {
	cache  *OriginalResolver
	export CurrentStillExporter
}

type currentStillCacheProof struct {
	Version     int    `json:"version"`
	Role        string `json:"role"`
	MediaType   string `json:"media_type"`
	Orientation int32  `json:"orientation"`
	PixelWidth  int64  `json:"pixel_width"`
	PixelHeight int64  `json:"pixel_height"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

func NewCurrentStillResolver(root string, exporter CurrentStillExporter) (*CurrentStillResolver, error) {
	if exporter == nil {
		return nil, errors.New("current-still exporter is required")
	}
	cache, err := newOriginalResolver(root, ProductOriginalCacheMaxBytes, func(context.Context, OriginalExportQuery, string, bool) error {
		return errors.New("current-still resolver never exports an immutable original")
	})
	if err != nil {
		return nil, err
	}
	return &CurrentStillResolver{cache: cache, export: exporter}, nil
}

func (r *CurrentStillResolver) Resolve(ctx context.Context, request CurrentStillRequest) (CurrentStillResolution, error) {
	startedAt := time.Now()
	if r == nil || r.cache == nil {
		return CurrentStillResolution{}, errors.New("current-still resolver is not configured")
	}
	request.SourceLibraryID = strings.TrimSpace(request.SourceLibraryID)
	request.AssetUUID = strings.ToLower(strings.TrimSpace(request.AssetUUID))
	if request.SourceLibraryID == "" || request.AssetUUID == "" || !request.Freshness.valid() {
		return CurrentStillResolution{}, errors.New("source library ID, asset UUID and exactly one valid current-still freshness token are required")
	}
	path := CurrentStillCachePath(r.cache.root, request.SourceLibraryID, request.AssetUUID, request.Freshness)
	queueStartedAt := time.Now()
	lock, err := r.cache.lock(ctx, path, syscall.LOCK_EX)
	if err != nil {
		timings := CurrentStillPhaseTimings{QueueWaitMicros: elapsedMicros(queueStartedAt)}
		return failedCurrentStillResolution(fmt.Errorf("lock current-still cache entry: %w", err), timings, startedAt, 0)
	}
	timings := CurrentStillPhaseTimings{QueueWaitMicros: elapsedMicros(queueStartedAt)}
	keepLock := false
	defer func() {
		if !keepLock {
			lock.Close()
		}
	}()
	validationStartedAt := time.Now()
	if fact, ok := inspectCachedCurrentStill(path); ok {
		timings.ValidationHashMicros = elapsedMicros(validationStartedAt)
		_ = os.Chtimes(path, time.Now(), time.Now())
		if err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_SH); err != nil {
			return failedCurrentStillResolution(fmt.Errorf("lock cached current still for use: %w", err), timings, startedAt, 0)
		}
		lease := &OriginalLease{resolver: r.cache, lock: lock.file}
		lock.file = nil
		keepLock = true
		timings.TotalMicros = elapsedMicros(startedAt)
		return CurrentStillResolution{Path: path, Source: CurrentStillSourceCache, Fact: fact, Timings: timings, Lease: lease}, nil
	}
	timings.ValidationHashMicros = elapsedMicros(validationStartedAt)
	removeCachedOriginal(path)
	temporary := originalCacheTemporaryPath(path)
	defer func() { _ = os.Remove(temporary); _ = os.Remove(temporary + ".exporting") }()
	fact, err := r.export(ctx, request, temporary)
	timings = mergeCurrentStillTimings(timings, fact.Timings)
	photoKitCalls := fact.PhotoKitCalls
	fact.Timings = CurrentStillPhaseTimings{}
	fact.PhotoKitCalls = 0
	if err != nil {
		removeCachedOriginal(path)
		var measured *CurrentStillMeasuredError
		if errors.As(err, &measured) {
			timings = mergeCurrentStillTimings(timings, measured.Timings)
			photoKitCalls = measured.PhotoKitCalls
		}
		return failedCurrentStillResolution(err, timings, startedAt, photoKitCalls)
	}
	validationStartedAt = time.Now()
	if err := validateCurrentStillFile(temporary, fact); err != nil {
		timings.ValidationHashMicros += elapsedMicros(validationStartedAt)
		removeCachedOriginal(path)
		return failedCurrentStillResolution(err, timings, startedAt, photoKitCalls)
	}
	timings.ValidationHashMicros += elapsedMicros(validationStartedAt)
	if fact.Size > r.cache.maxBytes {
		removeCachedOriginal(path)
		return failedCurrentStillResolution(fmt.Errorf("current still exceeds cache budget: %d bytes", fact.Size), timings, startedAt, photoKitCalls)
	}
	installationStartedAt := time.Now()
	if err := os.Chmod(temporary, 0o600); err != nil {
		timings.CacheInstallationMicros = elapsedMicros(installationStartedAt)
		return failedCurrentStillResolution(err, timings, startedAt, photoKitCalls)
	}
	if err := os.Rename(temporary, path); err != nil {
		removeCachedOriginal(path)
		timings.CacheInstallationMicros = elapsedMicros(installationStartedAt)
		return failedCurrentStillResolution(err, timings, startedAt, photoKitCalls)
	}
	if err := writeCurrentStillCacheProof(path, fact); err != nil {
		removeCachedOriginal(path)
		timings.CacheInstallationMicros = elapsedMicros(installationStartedAt)
		return failedCurrentStillResolution(err, timings, startedAt, photoKitCalls)
	}
	if err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_SH); err != nil {
		removeCachedOriginal(path)
		timings.CacheInstallationMicros = elapsedMicros(installationStartedAt)
		return failedCurrentStillResolution(err, timings, startedAt, photoKitCalls)
	}
	lease := &OriginalLease{resolver: r.cache, lock: lock.file}
	lock.file = nil
	keepLock = true
	if err := r.cache.prune(); err != nil {
		lease.Close()
		removeCachedOriginal(path)
		timings.CacheInstallationMicros = elapsedMicros(installationStartedAt)
		return failedCurrentStillResolution(err, timings, startedAt, photoKitCalls)
	}
	timings.CacheInstallationMicros = elapsedMicros(installationStartedAt)
	timings.TotalMicros = elapsedMicros(startedAt)
	return CurrentStillResolution{Path: path, Source: CurrentStillSourcePhotoKit, Fact: fact, Exported: true, Timings: timings, PhotoKitCalls: photoKitCalls, Lease: lease}, nil
}

func failedCurrentStillResolution(err error, timings CurrentStillPhaseTimings, startedAt time.Time, photoKitCalls int) (CurrentStillResolution, error) {
	timings.TotalMicros = elapsedMicros(startedAt)
	failure := measuredCurrentStillError(err, timings)
	failure.PhotoKitCalls = photoKitCalls
	return CurrentStillResolution{}, failure
}

func elapsedMicros(start time.Time) int64 {
	elapsed := time.Since(start)
	if elapsed <= 0 {
		return 1
	}
	return max((elapsed.Nanoseconds()+int64(time.Microsecond)-1)/int64(time.Microsecond), 1)
}

func mergeCurrentStillTimings(left, right CurrentStillPhaseTimings) CurrentStillPhaseTimings {
	left.HelperVerificationMicros += right.HelperVerificationMicros
	left.LaunchServicesStartMicros += right.LaunchServicesStartMicros
	left.PhotoKitCallbackMicros += right.PhotoKitCallbackMicros
	left.ValidationHashMicros += right.ValidationHashMicros
	return left
}

func measuredCurrentStillError(err error, timings CurrentStillPhaseTimings) *CurrentStillMeasuredError {
	var measured *CurrentStillMeasuredError
	photoKitCalls := 0
	if errors.As(err, &measured) {
		err = measured.Cause
		photoKitCalls = measured.PhotoKitCalls
	}
	return &CurrentStillMeasuredError{Cause: err, Timings: timings, PhotoKitCalls: photoKitCalls}
}

func validateCurrentStillFile(path string, want CurrentStillFact) error {
	info, digest, err := InspectOriginalFile(path)
	if err != nil {
		return fmt.Errorf("inspect current still: %w", err)
	}
	if want.MediaType == "" || want.PixelWidth <= 0 || want.PixelHeight <= 0 || want.Size != info.Size() || !strings.EqualFold(want.SHA256, hex.EncodeToString(digest[:])) {
		return errors.New("current-still output proof is invalid")
	}
	return nil
}

func inspectCachedCurrentStill(path string) (CurrentStillFact, bool) {
	data, err := os.ReadFile(originalCacheProofPath(path))
	if err != nil {
		return CurrentStillFact{}, false
	}
	var proof currentStillCacheProof
	if json.Unmarshal(data, &proof) != nil || proof.Version != 1 || proof.Role != "current_still" {
		return CurrentStillFact{}, false
	}
	fact := CurrentStillFact{MediaType: proof.MediaType, Orientation: proof.Orientation, PixelWidth: proof.PixelWidth, PixelHeight: proof.PixelHeight, Size: proof.Size, SHA256: proof.SHA256}
	if validateCurrentStillFile(path, fact) != nil {
		return CurrentStillFact{}, false
	}
	return fact, true
}

// ReadCachedCurrentStill opens an already proved current-still cache entry.
// It never creates a cache entry or falls back to PhotoKit.
func ReadCachedCurrentStill(root, sourceLibraryID, assetUUID string, freshness CurrentStillFreshness) (string, CurrentStillFact, string, bool) {
	if !freshness.valid() {
		return "", CurrentStillFact{}, "", false
	}
	path := CurrentStillCachePath(root, sourceLibraryID, assetUUID, freshness)
	fact, ok := inspectCachedCurrentStill(path)
	if !ok {
		return "", CurrentStillFact{}, "", false
	}
	proof, err := os.ReadFile(originalCacheProofPath(path))
	if err != nil {
		return "", CurrentStillFact{}, "", false
	}
	digest := sha256.Sum256(proof)
	return path, fact, hex.EncodeToString(digest[:]), true
}

func writeCurrentStillCacheProof(path string, fact CurrentStillFact) error {
	proof := currentStillCacheProof{Version: 1, Role: "current_still", MediaType: fact.MediaType, Orientation: fact.Orientation, PixelWidth: fact.PixelWidth, PixelHeight: fact.PixelHeight, Size: fact.Size, SHA256: fact.SHA256}
	data, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	return writeOriginalCacheProofData(path, append(data, '\n'))
}

func currentStillFactFromBytes(mediaType string, orientation int32, width, height int64, data []byte) CurrentStillFact {
	digest := sha256.Sum256(data)
	return CurrentStillFact{MediaType: mediaType, Orientation: orientation, PixelWidth: width, PixelHeight: height, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
}
