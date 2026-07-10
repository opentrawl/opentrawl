package evalcard

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

func TestNormalizeOllamaGenerateURL(t *testing.T) {
	tests := map[string]string{
		"":                                    DefaultOllamaGenerateURL,
		"http://127.0.0.1:11434":              "http://127.0.0.1:11434/api/generate",
		"http://127.0.0.1:11434/api":          "http://127.0.0.1:11434/api/generate",
		"http://127.0.0.1:11434/api/generate": "http://127.0.0.1:11434/api/generate",
	}
	for input, want := range tests {
		got, err := normalizeOllamaGenerateURL(input)
		if err != nil {
			t.Fatalf("normalizeOllamaGenerateURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeOllamaGenerateURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDefaultOutputDirIsPrivate(t *testing.T) {
	root := t.TempDir()
	got, err := defaultedOutputDir("", root, func() time.Time {
		return time.Date(2026, 5, 30, 12, 30, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "2026-05-30-123000-photo-card")
	if got != want {
		t.Fatalf("defaultedOutputDir = %q, want %q", got, want)
	}
}

func TestRejectRepoPath(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()

	if err := rejectRepoPath(filepath.Join(root, "evals")); err == nil {
		t.Fatal("rejectRepoPath accepted a repo-local output dir")
	}
	if err := rejectRepoPath(filepath.Join(t.TempDir(), "evals")); err != nil {
		t.Fatalf("rejectRepoPath rejected external output dir: %v", err)
	}
}

func TestPromptWithMetadataUsesTemplateFileText(t *testing.T) {
	got, err := promptWithMetadata("Prompt\n\n{{.MetadataJSON}}", []byte(`{"asset":"A"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Prompt\n\n{\"asset\":\"A\"}" {
		t.Fatalf("promptWithMetadata = %q", got)
	}
}

func TestOriginalRequestUsesOnlyPackageIndexForPackageCandidate(t *testing.T) {
	localIdentifier := "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001"
	request := originalRequest(photos.LocalMediaIndex{
		"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE": {{Path: "/synthetic/package/original.heic", Class: "original", Size: 42}},
	}, "/synthetic/library", photos.Asset{
		LocalIdentifier: localIdentifier,
		Resources: []photos.Resource{{
			Type:             "photo",
			OriginalFilename: "photo.jpeg",
			LocalPath:        "/synthetic/archive/metadata-only.jpeg",
			AvailableLocally: true,
		}},
	}, false)
	if len(request.PackageCandidates) != 1 || request.PackageCandidates[0].Path != "/synthetic/package/original.heic" {
		t.Fatalf("package candidates = %#v", request.PackageCandidates)
	}
	if request.SourceLibraryID != photos.SourceLibraryID("/synthetic/library") || request.AllowNetwork {
		t.Fatalf("request = %#v", request)
	}
}

func TestOriginalRequestUsesProductCacheIdentity(t *testing.T) {
	libraryPath := "/synthetic/Fixture Photos Library.photoslibrary"
	asset := photos.Asset{
		LocalIdentifier:  "synthetic-asset",
		ModificationDate: "2026-07-10T12:00:00Z",
		Resources: []photos.Resource{{
			Type:             "photo",
			OriginalFilename: "synthetic.heic",
			UTI:              "public.heic",
		}},
	}
	request := originalRequest(nil, libraryPath, asset, false)
	productPath := photos.OriginalCachePath("cache", photos.SourceLibraryID(libraryPath), asset.ModificationDate, request.Query)
	evalPath := photos.OriginalCachePath("cache", request.SourceLibraryID, asset.ModificationDate, request.Query)
	if evalPath != productPath {
		t.Fatalf("eval cache path = %q, product cache path = %q", evalPath, productPath)
	}
}

func TestOriginalRequestUsesCameraOriginalAndNetworkChoice(t *testing.T) {
	request := originalRequest(nil, "synthetic-library", photos.Asset{
		LocalIdentifier: "synthetic-asset",
		Resources: []photos.Resource{
			{Type: "alternate_photo", OriginalFilename: "alternate.jpeg", NeedsDownload: true},
			{Type: "full_size_photo", OriginalFilename: "full-size.heic"},
			{Type: "photo", OriginalFilename: "camera-original.dng", UTI: "com.adobe.raw-image"},
		},
	}, true)
	if !request.AllowNetwork || request.Query.OriginalFilename != "camera-original.dng" {
		t.Fatalf("request = %#v", request)
	}
}

func TestOriginalRequestDoesNotUseEditedResourceAsOriginal(t *testing.T) {
	request := originalRequest(nil, "synthetic-library", photos.Asset{
		LocalIdentifier: "synthetic-asset",
		Resources: []photos.Resource{{
			Type:             "full_size_photo",
			OriginalFilename: "full-size.heic",
			UTI:              "public.heic",
		}},
	}, true)
	if request.Query.OriginalFilename != "" || request.Query.OriginalUTI != "" {
		t.Fatalf("edited resource reached original query: %#v", request.Query)
	}
}
