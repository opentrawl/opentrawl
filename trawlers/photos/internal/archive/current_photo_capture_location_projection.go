package archive

import (
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/proto"
)

func currentPhotoCaptureLocationEvidenceFromOutcome(outcome *locationwire.ComposePhotoLocationEvidenceOutcome) *locationwire.PhotoLocationBriefing {
	if outcome == nil || !composedPhotoLocationEvidenceIsCurrent(outcome) {
		return nil
	}
	briefing := outcome.GetBriefing()
	if briefing == nil {
		return nil
	}
	return proto.Clone(briefing).(*locationwire.PhotoLocationBriefing)
}
