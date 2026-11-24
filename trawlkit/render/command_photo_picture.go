package render

import (
	"fmt"
	"io"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

func photoPictureStateForHumanOutput(record *photo.OpenedPhotoRecord) string {
	fetchedPicture := record.GetPictureFetchResult()
	switch fetchedPicture.GetPictureFetchOutcome() {
	case photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED:
		if record.GetCachedPictureFilePath() != "" {
			return record.GetCachedPictureFilePath()
		}
		return "Fetched; no longer in the picture cache."
	case photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED:
		return "Fetching the picture failed: " + fetchedPicture.GetPictureFetchFailureMessage()
	default:
		return "Not fetched yet."
	}
}

// writePhotoPictureLine leaves a cached picture's path unwrapped, so it works
// with one copy and paste.
func writePhotoPictureLine(writer io.Writer, record *photo.OpenedPhotoRecord) error {
	if record.GetPictureFetchResult().GetPictureFetchOutcome() == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED && record.GetCachedPictureFilePath() != "" {
		return writeLine(writer, "Picture: "+record.GetCachedPictureFilePath())
	}
	return writeLines(writer, Wrap("Picture: "+photoPictureStateForHumanOutput(record), OutputWidth(writer)))
}

func photoPictureSourceForHumanOutput(pictureSource photo.PictureSource) string {
	switch pictureSource {
	case photo.PictureSource_PICTURE_SOURCE_PICTURE_MIRROR_FOLDER:
		return "Picture mirror folder"
	case photo.PictureSource_PICTURE_SOURCE_APPLE_PHOTOS:
		return "Apple Photos"
	default:
		return ""
	}
}

func photoCameraBearingForHumanOutput(fetchedPicture *photo.PictureFetchResult) string {
	if fetchedPicture.CameraBearingDegrees == nil {
		return "Not in the picture file"
	}
	bearing := fmt.Sprintf("%.1f°", fetchedPicture.GetCameraBearingDegrees())
	switch fetchedPicture.GetCameraBearingReference() {
	case photo.CameraBearingReference_CAMERA_BEARING_REFERENCE_TRUE_NORTH:
		return bearing + " from true north"
	case photo.CameraBearingReference_CAMERA_BEARING_REFERENCE_MAGNETIC_NORTH:
		return bearing + " from magnetic north"
	default:
		return bearing + " from a north the file does not name"
	}
}

func inspectedPictureFields(record *photo.OpenedPhotoRecord) []cardField {
	fetchedPicture := record.GetPictureFetchResult()
	editShown := "Not edited"
	if fetchedPicture.GetFetchedForAppleAdjustmentsState() != 0 {
		if editTime := formatExactUTCTimeForInspection(fetchedPicture.GetFetchedForAppleAdjustmentTimeUtc()); editTime != "" {
			editShown = fmt.Sprintf("Edit of %s (Apple editing state code %d)", editTime, fetchedPicture.GetFetchedForAppleAdjustmentsState())
		} else {
			editShown = fmt.Sprintf("Edited (Apple editing state code %d)", fetchedPicture.GetFetchedForAppleAdjustmentsState())
		}
	}
	fields := []cardField{
		{label: "Source", value: photoPictureSourceForHumanOutput(fetchedPicture.GetPictureSource())},
		{label: "Apple edit shown", value: editShown},
	}
	if fetchedPicture.GetPictureFetchOutcome() == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED {
		retry := "Only with --retry-failed"
		if fetchedPicture.GetRetryNotBeforeTimeUtc() != nil {
			retry = "After " + formatExactUTCTimeForInspection(fetchedPicture.GetRetryNotBeforeTimeUtc())
		}
		return append(fields, cardField{label: "Failure", value: fetchedPicture.GetPictureFetchFailureMessage()}, cardField{label: "Tried again", value: retry})
	}
	fields = append(fields,
		cardField{label: "SHA-256", value: fetchedPicture.GetPictureSha256()},
		cardField{label: "Picture size", value: formatByteCount(uint64(fetchedPicture.GetPictureByteCount()))},
	)
	if fetchedPicture.PictureLongEdgePixelLimitUsed != nil {
		fields = append(fields, cardField{
			label: "Long edge limit",
			value: FormatInteger(int64(fetchedPicture.GetPictureLongEdgePixelLimitUsed())) + " px",
		})
	}
	return append(fields, cardField{label: "Camera bearing", value: photoCameraBearingForHumanOutput(fetchedPicture)})
}

func writePhotoPictureInspection(writer io.Writer, record *photo.OpenedPhotoRecord, link string) error {
	fetchPictureAgain := nextCommandWithLabel(nextCommandLabelFetchPicture, "photos", "run", "fetch_picture", "--photo", link)
	switch record.GetPictureFetchResult().GetPictureFetchOutcome() {
	case photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED:
		if record.GetCachedPictureFilePath() != "" {
			fetchPictureAgain = NextCommand{}
		}
	case photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED:
		if record.GetPictureFetchResult().GetRetryNotBeforeTimeUtc() == nil {
			fetchPictureAgain.trawlCommandArgumentsAfterInvocation = append(fetchPictureAgain.trawlCommandArgumentsAfterInvocation, "--retry-failed")
		}
	default:
		if err := writeLine(writer, "Picture metadata: the step has not run for this photo."); err != nil {
			return err
		}
		return writeNextCommands(writer, []NextCommand{fetchPictureAgain, nextCommandOpeningRecord(link)})
	}
	if err := writeCard(writer, card{title: "Picture metadata", fields: inspectedPictureFields(record)}); err != nil {
		return err
	}
	if record.GetPictureFetchResult().GetPictureFetchOutcome() == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED {
		if err := writePhotoPictureLine(writer, record); err != nil {
			return err
		}
	}
	return writeNextCommands(writer, []NextCommand{fetchPictureAgain, nextCommandOpeningRecord(link)})
}
