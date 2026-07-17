package telegramdesktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveImportSourceReportsMissingPostbox(t *testing.T) {
	postbox := filepath.Join(t.TempDir(), "missing-postbox")
	source := resolveImportSourcePath(context.Background(), "", postbox)
	var unavailable *SourceUnavailableError
	if !errors.As(source.unavailable, &unavailable) || unavailable.State != "missing" {
		t.Fatalf("unavailable = %v, want missing SourceUnavailableError", source.unavailable)
	}
	if source.path != postbox || source.product != sourceProductNative {
		t.Fatalf("source = %+v", source)
	}
}

func TestResolveImportSourceReportsUnreadablePostbox(t *testing.T) {
	postbox := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(postbox, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := resolveImportSourcePath(context.Background(), "", postbox)
	var unavailable *SourceUnavailableError
	if !errors.As(source.unavailable, &unavailable) || unavailable.State != "unreadable" {
		t.Fatalf("unavailable = %v, want unreadable SourceUnavailableError", source.unavailable)
	}
}

func TestResolveImportSourceRejectsTelegramDesktopData(t *testing.T) {
	desktop := t.TempDir()
	if err := os.WriteFile(filepath.Join(desktop, "key_datas"), []byte("TDF$synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := resolveImportSourcePath(context.Background(), desktop, "unused-default")
	var unavailable *SourceUnavailableError
	if !errors.As(source.unavailable, &unavailable) {
		t.Fatalf("unavailable = %v, want SourceUnavailableError", source.unavailable)
	}
}
