package cli

import (
	"net/http"
	"strings"
	"testing"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

func TestRemoteAppPhotoUnavailableErrorSurfacesSafeFailureReason(t *testing.T) {
	statusCode, message := remoteAppPhotoUnavailableError(&photo.OpenedPhotoRecord{
		PictureFetchResult: &photo.PictureFetchResult{
			PictureFetchOutcome:        photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED,
			PictureFetchFailureMessage: "OpenTrawl does not have full access to Apple Photos.",
		},
	})
	if statusCode != http.StatusServiceUnavailable {
		t.Fatalf("statusCode = %d, want %d", statusCode, http.StatusServiceUnavailable)
	}
	if message != "OpenTrawl does not have full access to Apple Photos." {
		t.Fatalf("message = %q", message)
	}

	missingCode, missingMessage := remoteAppPhotoUnavailableError(&photo.OpenedPhotoRecord{
		PictureFetchResult: &photo.PictureFetchResult{
			PictureFetchOutcome:        photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED,
			PictureFetchFailureMessage: "Apple Photos has no photo with this UUID",
		},
	})
	if missingCode != http.StatusNotFound {
		t.Fatalf("missingCode = %d, want %d", missingCode, http.StatusNotFound)
	}
	if missingMessage != "Apple Photos has no photo with this UUID" {
		t.Fatalf("missingMessage = %q", missingMessage)
	}

	_, sanitized := remoteAppPhotoUnavailableError(&photo.OpenedPhotoRecord{
		PictureFetchResult: &photo.PictureFetchResult{
			PictureFetchOutcome:        photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED,
			PictureFetchFailureMessage: "open /Volumes/Archive/2026/photo.heic: no such file",
		},
	})
	if strings.Contains(sanitized, "/Volumes/") {
		t.Fatalf("sanitized message leaked server path: %q", sanitized)
	}
}
