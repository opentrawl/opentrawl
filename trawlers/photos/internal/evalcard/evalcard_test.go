package evalcard

import (
	"context"
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

func TestResolveOriginalUsesPackageOriginalWithoutPhotoKit(t *testing.T) {
	localIdentifier := "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001"
	originalPath := filepath.Join(t.TempDir(), "original.heic")
	if err := os.WriteFile(originalPath, []byte("synthetic original"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldExport := exportOriginalResource
	exportOriginalResource = func(context.Context, photos.OriginalExportQuery, string, bool) error {
		t.Fatal("package-local original reached PhotoKit")
		return nil
	}
	defer func() { exportOriginalResource = oldExport }()

	path, source, err := resolveOriginal(context.Background(), photos.LocalMediaIndex{
		"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE": {{Path: originalPath, Class: "original", Size: 18}},
	}, t.TempDir(), photos.Asset{LocalIdentifier: localIdentifier}, true)
	if err != nil {
		t.Fatal(err)
	}
	if path != originalPath || source != "photos_package_original" {
		t.Fatalf("resolved path = %q source = %q", path, source)
	}
}

func TestResolveOriginalDoesNotTreatArchiveLocalityAsPackageOriginal(t *testing.T) {
	_, _, err := resolveOriginal(context.Background(), nil, t.TempDir(), photos.Asset{
		LocalIdentifier: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001",
		Resources: []photos.Resource{{
			Type:             "photo",
			LocalPath:        "/synthetic/archive/path.jpeg",
			AvailableLocally: true,
			NeedsDownload:    false,
		}},
	}, false)
	if err == nil || err.Error() != "missing_original" {
		t.Fatalf("resolve error = %v, want missing_original", err)
	}
}

func TestResolveOriginalExportsOnlyWithICloudAllowed(t *testing.T) {
	oldExport := exportOriginalResource
	exportOriginalResource = func(_ context.Context, query photos.OriginalExportQuery, destination string, allowNetwork bool) error {
		if query.LocalIdentifier != "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001" || !allowNetwork {
			t.Fatalf("export input = %#v allow network = %t", query, allowNetwork)
		}
		return os.WriteFile(destination, []byte("synthetic PhotoKit original"), 0o600)
	}
	defer func() { exportOriginalResource = oldExport }()

	cacheDir := t.TempDir()
	path, source, err := resolveOriginal(context.Background(), nil, cacheDir, photos.Asset{
		LocalIdentifier: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if source != "photokit_original_export" {
		t.Fatalf("source = %q", source)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("exported original = %q info = %#v err = %v", path, info, err)
	}
}

func TestResolveOriginalRejectsEmptyPhotoKitOutput(t *testing.T) {
	oldExport := exportOriginalResource
	exportOriginalResource = func(context.Context, photos.OriginalExportQuery, string, bool) error { return nil }
	defer func() { exportOriginalResource = oldExport }()

	_, _, err := resolveOriginal(context.Background(), nil, t.TempDir(), photos.Asset{
		LocalIdentifier: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE/L0/001",
	}, true)
	if err == nil || err.Error() != "export_original" {
		t.Fatalf("resolve error = %v, want export_original", err)
	}
}
