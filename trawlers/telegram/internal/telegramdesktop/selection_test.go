package telegramdesktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveImportSourceKeepsNativeSelectionWhenPostboxIsUnavailable(t *testing.T) {
	tdata := filepath.Join(t.TempDir(), "tdata")
	if err := os.MkdirAll(tdata, 0o700); err != nil {
		t.Fatal(err)
	}
	postbox := filepath.Join(t.TempDir(), "missing-postbox")

	source := resolveImportSourcePaths(context.Background(), "", tdata, postbox, true)
	var unavailable *SourceUnavailableError
	if !errors.As(source.unavailable, &unavailable) {
		t.Fatalf("unavailable = %v, want SourceUnavailableError", source.unavailable)
	}
	if source.path != postbox || !source.postbox || source.product != sourceProductNative || unavailable.State != "missing" {
		t.Fatalf("source = %+v unavailable = %+v", source, unavailable)
	}
	t.Logf("resolver input native_installed=true desktop=%q postbox=%q", tdata, postbox)
	t.Logf("resolver output product=%q path=%q postbox=%t unavailable=%q", source.product, source.path, source.postbox, unavailable.State)
}

func TestResolveImportSourceReportsUnreadableNativePostbox(t *testing.T) {
	tdata := filepath.Join(t.TempDir(), "tdata")
	if err := os.MkdirAll(tdata, 0o700); err != nil {
		t.Fatal(err)
	}
	postbox := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(postbox, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}

	source := resolveImportSourcePaths(context.Background(), "", tdata, postbox, true)
	var unavailable *SourceUnavailableError
	if !errors.As(source.unavailable, &unavailable) || unavailable.State != "unreadable" {
		t.Fatalf("unavailable = %v, want unreadable SourceUnavailableError", source.unavailable)
	}
	if source.path != postbox || !source.postbox || source.product != sourceProductNative {
		t.Fatalf("source = %+v", source)
	}
	t.Logf("resolver input native_installed=true desktop=%q postbox=%q", tdata, postbox)
	t.Logf("resolver output product=%q path=%q postbox=%t unavailable=%q", source.product, source.path, source.postbox, unavailable.State)
}

func TestResolveImportSourcePrefersNativeWhenBothProductsAreReadable(t *testing.T) {
	postbox, _, _ := makePostboxFixture(t)
	tdata := filepath.Join(t.TempDir(), "tdata")
	if err := os.MkdirAll(tdata, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tdata, "key_datas"), []byte("TDF$synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}

	source := resolveImportSourcePaths(context.Background(), "", tdata, postbox, true)
	if source.unavailable != nil || source.path != postbox || !source.postbox || source.product != sourceProductNative {
		t.Fatalf("source = %+v", source)
	}
	t.Logf("resolver input native_installed=true desktop=%q postbox=%q", tdata, postbox)
	t.Logf("resolver output product=%q path=%q postbox=%t unavailable=%v", source.product, source.path, source.postbox, source.unavailable)
}

func TestNativeTelegramInstallationRequiresNativeBundleID(t *testing.T) {
	paths := []string{"/synthetic/Applications/Telegram.app", "/synthetic/Applications/Other.app"}
	identifiers := map[string]string{
		paths[0]: nativeTelegramBundleID,
		paths[1]: "org.example.Other",
	}
	installed := nativeTelegramInstalledAt(paths, func(path string) (string, error) {
		value, ok := identifiers[path]
		if !ok {
			return "", os.ErrNotExist
		}
		return value, nil
	})
	if !installed {
		t.Fatal("native Telegram bundle was not detected")
	}
	t.Logf("bundle detector input paths=%q identifiers=%q", paths, identifiers)
	t.Logf("bundle detector output native_installed=%t", installed)
}

func TestNativeTelegramBundleIDAtReadsSyntheticInfoPlist(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil is a macOS process boundary")
	}
	appPath := filepath.Join(t.TempDir(), "Telegram.app")
	infoPath := filepath.Join(appPath, "Contents", "Info.plist")
	if err := os.MkdirAll(filepath.Dir(infoPath), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>ru.keepcoder.Telegram</string></dict></plist>`)
	if err := os.WriteFile(infoPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	bundleID, err := nativeTelegramBundleIDAt(appPath)
	if err != nil {
		t.Fatal(err)
	}
	if bundleID != nativeTelegramBundleID+"\n" {
		t.Fatalf("bundle id = %q, want %q", bundleID, nativeTelegramBundleID+"\n")
	}
	t.Logf("plutil input path=%q", infoPath)
	t.Logf("plutil output stdout=%q stderr=%q exit=%d", bundleID, "", 0)
}
