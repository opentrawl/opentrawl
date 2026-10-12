package applelibrary

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

// The evaluation set is private: real photos from the owner's library and
// their verified facts. It lives outside this repository.
const photosEvaluationSetDirectoryEnvironmentKey = "OPENTRAWL_PHOTOS_EVALUATION_SET_DIRECTORY"

func TestReadAppleLibraryStepReadsEvaluationPhotosWithTheirVerifiedFacts(t *testing.T) {
	evaluationSetDirectory := os.Getenv(photosEvaluationSetDirectoryEnvironmentKey)
	if evaluationSetDirectory == "" {
		t.Skip(photosEvaluationSetDirectoryEnvironmentKey + " is not set")
	}
	expectedSnapshotText, err := os.ReadFile(filepath.Join(evaluationSetDirectory, "read_apple_library_expected.txtpb"))
	if err != nil {
		t.Fatal(err)
	}
	expectedSnapshot := &photosstep.AppleLibrarySnapshot{}
	if err := prototext.Unmarshal(expectedSnapshotText, expectedSnapshot); err != nil {
		t.Fatal(err)
	}
	expectedFactsByUUID := map[string]*photo.AppleLibraryPhotoFacts{}
	var evaluationPhotoUUIDs []string
	for _, expectedFacts := range expectedSnapshot.GetAppleLibraryPhotosNewestFirst() {
		expectedFactsByUUID[expectedFacts.GetApplePhotoUuid()] = expectedFacts
		evaluationPhotoUUIDs = append(evaluationPhotoUUIDs, expectedFacts.GetApplePhotoUuid())
	}
	applePhotosLibraryPath, err := DefaultApplePhotosLibraryPath()
	if err != nil {
		t.Fatal(err)
	}
	appleLibrarySnapshot, err := ReadAppleLibraryStep{}.RunPhotosStepOnOneInput(context.Background(), &photosstep.ReadAppleLibraryRequest{
		ApplePhotosLibraryPath:  applePhotosLibraryPath,
		SelectedApplePhotoUuids: evaluationPhotoUUIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, readFacts := range appleLibrarySnapshot.GetAppleLibraryPhotosNewestFirst() {
		expectedFacts := withoutFactsAppleRecomputes(expectedFactsByUUID[readFacts.GetApplePhotoUuid()])
		readFacts = withoutFactsAppleRecomputes(readFacts)
		if !proto.Equal(expectedFacts, readFacts) {
			t.Errorf("photo %s\nexpected:\n%s\nread:\n%s", readFacts.GetApplePhotoUuid(),
				prototext.Format(expectedFacts), prototext.Format(readFacts))
		}
	}
}

// withoutFactsAppleRecomputes clears what Apple or the owner changes after a
// photo is taken: edits, albums, names, moments and trips. Apple's recognised
// text stays in the comparison: its decoder is the most fragile part of the
// step, and Apple rarely recognises a photo again.
func withoutFactsAppleRecomputes(applePhotoFacts *photo.AppleLibraryPhotoFacts) *photo.AppleLibraryPhotoFacts {
	stableFacts := proto.Clone(applePhotoFacts).(*photo.AppleLibraryPhotoFacts)
	stableFacts.AppleModificationTimeUtc = nil
	stableFacts.AppleAdjustmentsState = 0
	stableFacts.AppleAlbums = nil
	stableFacts.AppleNamedPeople = nil
	stableFacts.AppleMomentUuid = ""
	stableFacts.AppleMomentTitle = ""
	stableFacts.AppleTripUuids = nil
	stableFacts.AppleCaption = ""
	stableFacts.AppleTitle = ""
	stableFacts.IsFavourite = false
	stableFacts.IsHidden = false
	return stableFacts
}
