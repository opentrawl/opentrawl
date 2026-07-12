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
	Modification    CurrentStillModification
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

type CurrentStillFact struct {
	MediaType   string
	Orientation int32
	PixelWidth  int64
	PixelHeight int64
	Size        int64
	SHA256      string
}

type CurrentStillResolution struct {
	Path     string
	Source   string
	Fact     CurrentStillFact
	Exported bool
	Lease    *OriginalLease
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
	if r == nil || r.cache == nil {
		return CurrentStillResolution{}, errors.New("current-still resolver is not configured")
	}
	request.SourceLibraryID = strings.TrimSpace(request.SourceLibraryID)
	request.AssetUUID = strings.ToLower(strings.TrimSpace(request.AssetUUID))
	if request.SourceLibraryID == "" || request.AssetUUID == "" || !request.Modification.valid() {
		return CurrentStillResolution{}, errors.New("source library ID, asset UUID and canonical modification instant are required for current-still resolution")
	}
	path := CurrentStillCachePath(r.cache.root, request.SourceLibraryID, request.AssetUUID, request.Modification)
	lock, err := r.cache.lock(ctx, path, syscall.LOCK_EX)
	if err != nil {
		return CurrentStillResolution{}, fmt.Errorf("lock current-still cache entry: %w", err)
	}
	keepLock := false
	defer func() {
		if !keepLock {
			lock.Close()
		}
	}()
	if fact, ok := inspectCachedCurrentStill(path); ok {
		_ = os.Chtimes(path, time.Now(), time.Now())
		if err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_SH); err != nil {
			return CurrentStillResolution{}, fmt.Errorf("lock cached current still for use: %w", err)
		}
		lease := &OriginalLease{resolver: r.cache, lock: lock.file}
		lock.file = nil
		keepLock = true
		return CurrentStillResolution{Path: path, Source: CurrentStillSourceCache, Fact: fact, Lease: lease}, nil
	}
	removeCachedOriginal(path)
	temporary := originalCacheTemporaryPath(path)
	defer func() { _ = os.Remove(temporary); _ = os.Remove(temporary + ".exporting") }()
	fact, err := r.export(ctx, request, temporary)
	if err != nil {
		removeCachedOriginal(path)
		return CurrentStillResolution{}, err
	}
	if err := validateCurrentStillFile(temporary, fact); err != nil {
		removeCachedOriginal(path)
		return CurrentStillResolution{}, err
	}
	if fact.Size > r.cache.maxBytes {
		removeCachedOriginal(path)
		return CurrentStillResolution{}, fmt.Errorf("current still exceeds cache budget: %d bytes", fact.Size)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return CurrentStillResolution{}, err
	}
	if err := os.Rename(temporary, path); err != nil {
		removeCachedOriginal(path)
		return CurrentStillResolution{}, err
	}
	if err := writeCurrentStillCacheProof(path, fact); err != nil {
		removeCachedOriginal(path)
		return CurrentStillResolution{}, err
	}
	if err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_SH); err != nil {
		removeCachedOriginal(path)
		return CurrentStillResolution{}, err
	}
	lease := &OriginalLease{resolver: r.cache, lock: lock.file}
	lock.file = nil
	keepLock = true
	if err := r.cache.prune(); err != nil {
		lease.Close()
		removeCachedOriginal(path)
		return CurrentStillResolution{}, err
	}
	return CurrentStillResolution{Path: path, Source: CurrentStillSourcePhotoKit, Fact: fact, Exported: true, Lease: lease}, nil
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
