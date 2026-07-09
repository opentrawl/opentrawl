package avatar

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "avatar.png")
	writePNG(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got, err := InspectBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.MIME != "image/png" || got.SHA256 == "" || string(got.Data) != string(data) {
		t.Fatalf("avatar = %#v", got)
	}

	got.Data[0] = 0
	if data[0] == 0 {
		t.Fatal("InspectBytes returned caller-owned data")
	}
}

func TestInspectBytesRejectsEmptyData(t *testing.T) {
	if _, err := InspectBytes(nil); err == nil {
		t.Fatal("expected empty data error")
	}
}

func writePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{B: 255, A: 255})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
