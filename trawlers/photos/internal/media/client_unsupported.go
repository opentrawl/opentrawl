//go:build !darwin

package media

import (
	"context"
	"errors"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
)

type Client struct{}
type CurrentRenderedStillLease struct {
	Outcome *mediawire.CurrentRenderedStillLease
}
type PhotosMediaOutcomeError struct{}

func (e *PhotosMediaOutcomeError) Error() string { return "OpenTrawl Photos media requires macOS" }
func NewInstalledOpenTrawlClient() Client        { return Client{} }
func (Client) ReadPhotoLibraryAccess(context.Context) (*mediawire.PhotoLibraryAccessResult, error) {
	return nil, errors.New("OpenTrawl Photos media requires macOS")
}
func (Client) RequestPhotoLibraryAccess(context.Context) (*mediawire.PhotoLibraryAccessResult, error) {
	return nil, errors.New("OpenTrawl Photos media requires macOS")
}
func (Client) InspectPhotoAssetReadiness(context.Context, string) (*mediawire.PhotoAssetReadiness, error) {
	return nil, errors.New("OpenTrawl Photos media requires macOS")
}
func (Client) InspectImmutableOriginalImageFacts(context.Context, *mediawire.InspectImmutableOriginalImageFactsRequest) (*mediawire.ImmutableOriginalImageFacts, error) {
	return nil, errors.New("OpenTrawl Photos media requires macOS")
}
func (Client) AcquireCurrentRenderedStill(context.Context, *mediawire.AcquireCurrentRenderedStillRequest) (*CurrentRenderedStillLease, error) {
	return nil, errors.New("OpenTrawl Photos media requires macOS")
}
func (l *CurrentRenderedStillLease) Read() ([]byte, error) {
	return nil, errors.New("OpenTrawl Photos media requires macOS")
}
func (l *CurrentRenderedStillLease) Verify() error {
	return errors.New("OpenTrawl Photos media requires macOS")
}
func (l *CurrentRenderedStillLease) Close() error { return nil }
