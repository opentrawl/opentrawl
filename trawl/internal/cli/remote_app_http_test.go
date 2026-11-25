package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
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

func TestRemoteAppPhotoOpenFailureErrorDoesNotTurnTimeoutsIntoNotFound(t *testing.T) {
	timeoutCode, timeoutMessage := remoteAppPhotoOpenFailureError(&federation.TrawlerOperationFailure{
		FailureCode:    federation.FailureCode_FAILURE_CODE_TIMEOUT,
		FailureMessage: "context deadline exceeded",
	})
	if timeoutCode != http.StatusGatewayTimeout {
		t.Fatalf("timeout statusCode = %d, want %d", timeoutCode, http.StatusGatewayTimeout)
	}
	if !strings.Contains(strings.ToLower(timeoutMessage), "timed out") {
		t.Fatalf("timeout message = %q, want timeout explanation", timeoutMessage)
	}

	notFoundCode, _ := remoteAppPhotoOpenFailureError(&federation.TrawlerOperationFailure{
		FailureCode:    federation.FailureCode_FAILURE_CODE_NOT_FOUND,
		FailureMessage: "No photo has that link.",
	})
	if notFoundCode != http.StatusNotFound {
		t.Fatalf("notFound statusCode = %d, want %d", notFoundCode, http.StatusNotFound)
	}

	libraryNotFoundCode, _ := remoteAppPhotoOpenFailureError(&federation.TrawlerOperationFailure{
		FailureCode:    federation.FailureCode_FAILURE_CODE_LIBRARY_WORDED_OPERATION_FAILURE,
		FailureMessage: "No photo has that link.",
	})
	if libraryNotFoundCode != http.StatusNotFound {
		t.Fatalf("libraryNotFound statusCode = %d, want %d", libraryNotFoundCode, http.StatusNotFound)
	}

	unavailableCode, unavailableMessage := remoteAppPhotoOpenFailureError(&federation.TrawlerOperationFailure{
		FailureCode:    federation.FailureCode_FAILURE_CODE_UNAVAILABLE,
		FailureMessage: "open /Users/example/.opentrawl/photos/photos.db: permission denied",
	})
	if unavailableCode != http.StatusServiceUnavailable {
		t.Fatalf("unavailable statusCode = %d, want %d", unavailableCode, http.StatusServiceUnavailable)
	}
	if strings.Contains(unavailableMessage, "/Users/") {
		t.Fatalf("unavailable message leaked path: %q", unavailableMessage)
	}
}

func TestStripServerLocalPicturePathNormalizesRemotePhotoPictureField(t *testing.T) {
	for _, initialText := range []string{
		"/Users/example/.opentrawl/photos/picture-cache/abc.jpg",
		"Not fetched yet.",
		"Fetched; no longer in the picture cache.",
	} {
		response := &open.OpenResponse{
			Record: &open.OpenRecord{
				RecordTrawler: trawlkit.NewRegisteredTrawlerIdentity("photos"),
				TypedOpenedRecord: &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
					TrawlerSpecificOpenedRecordPresentation: &open.TrawlerSpecificOpenedRecordPresentation{
						DetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
							FieldsInDisplayOrder: []*presentation.TrawlerSpecificCommandDetailPresentationField{{
								FieldDisplayName: "Picture",
								FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
									TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{
										Text: initialText,
									},
								},
							}},
						},
					},
				},
			},
		}
		stripServerLocalPicturePathFromRemoteAppOpenResponse(response)
		got := response.GetRecord().GetTrawlerSpecificOpenedRecordPresentation().GetDetailPresentation().GetFieldsInDisplayOrder()[0].GetFieldValue().GetText()
		if got != "Available via remote photo endpoint." {
			t.Fatalf("initial %q -> got %q, want %q", initialText, got, "Available via remote photo endpoint.")
		}
	}
}
