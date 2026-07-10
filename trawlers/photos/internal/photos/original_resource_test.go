package photos

import "testing"

func TestPreferredOriginalResourceSelectsCameraOriginal(t *testing.T) {
	resources := []Resource{
		{Type: "alternate_photo", OriginalFilename: "alternate.jpeg", NeedsDownload: true},
		{Type: "full_size_photo", OriginalFilename: "full-size.jpeg"},
		{Type: "adjustment_base_photo", OriginalFilename: "base.jpeg", NeedsDownload: true},
		{Type: "photo", OriginalFilename: "photo.jpeg"},
	}
	got, ok := PreferredOriginalResource(resources)
	if !ok || got.Type != "photo" || got.OriginalFilename != "photo.jpeg" {
		t.Fatalf("preferred resource = %#v ok=%t", got, ok)
	}
}

func TestPreferredOriginalResourceRejectsEditedSubstitutes(t *testing.T) {
	resources := []Resource{
		{Type: "full_size_photo", OriginalFilename: "full-size.jpeg"},
		{Type: "alternate_photo", OriginalFilename: "alternate.jpeg"},
		{Type: "adjustment_base_photo", OriginalFilename: "base.jpeg"},
		{Type: "resource_type_99", OriginalFilename: "unknown.jpeg", UTI: "public.jpeg"},
	}
	if got, ok := PreferredOriginalResource(resources); ok {
		t.Fatalf("edited resource accepted as camera original: %#v", got)
	}
}

func TestPreferredOriginalResourceIgnoresAvailabilityFlags(t *testing.T) {
	resources := []Resource{
		{Type: "photo", OriginalFilename: "photo.jpeg", NeedsDownload: true},
		{Type: "alternate_photo", OriginalFilename: "alternate.jpeg", NeedsDownload: true},
	}
	got, ok := PreferredOriginalResource(resources)
	if !ok || got.Type != "photo" {
		t.Fatalf("preferred resource = %#v ok=%t", got, ok)
	}
}
