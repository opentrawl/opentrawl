package updatephotos

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
)

func retainedLunaThreadIdentifier(operation archive.RetainedPhotoModelGenerationOperation) string {
	if operation.State != archive.PhotoModelGenerationStateTransmissionStarted {
		return ""
	}
	return strings.TrimSpace(operation.ThreadIdentifier)
}

func retainedLunaTurnIdentifier(operation archive.RetainedPhotoModelGenerationOperation) string {
	if operation.State != archive.PhotoModelGenerationStateTransmissionStarted {
		return ""
	}
	return strings.TrimSpace(operation.TurnIdentifier)
}
