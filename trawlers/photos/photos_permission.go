package photoscrawl

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

var photosAccessStatus = photos.PhotoLibraryAccessStatusThroughApp

func (c *Crawler) photosSetupRequirements(ctx context.Context) []control.SetupRequirement {
	if !trawlkit.IsInternalAppRequest(ctx) {
		return nil
	}
	return []control.SetupRequirement{photosAccessRequirement(ctx)}
}

// RequestPhotosAccess is the app-only action. Its caller is responsible for
// presenting the resulting typed source status, not the helper's text.
func (c *Crawler) RequestPhotosAccess(ctx context.Context) (control.SetupRequirement, error) {
	status, err := photosAccessStatus(ctx, true)
	if err != nil {
		return control.SetupRequirement{}, err
	}
	return photosAccessSetupRequirement(status), nil
}

func photosAccessRequirement(ctx context.Context) control.SetupRequirement {
	status, err := photosAccessStatus(ctx, false)
	if err != nil {
		return control.NewSetupRequirement(
			"photos_access_unavailable",
			control.SetupKindPhotosPermission,
			control.SetupStateUnavailable,
			"Photos access could not be checked.",
			control.SetupActionNone,
			nil,
		)
	}
	return photosAccessSetupRequirement(status)
}

func photosAccessSetupRequirement(status string) control.SetupRequirement {
	switch status {
	case "not_determined":
		return control.NewSetupRequirement("photos_access_not_determined", control.SetupKindPhotosPermission, control.SetupStateNeedsAction, "Photos access has not been requested.", control.SetupActionRequestPhotos, nil)
	case "restricted":
		return control.NewSetupRequirement("photos_access_restricted", control.SetupKindPhotosPermission, control.SetupStateUnavailable, "Screen Time or your organisation’s policy prevents Photos access.", control.SetupActionNone, nil)
	case "denied":
		return control.NewSetupRequirement("photos_access_denied", control.SetupKindPhotosPermission, control.SetupStateNeedsAction, "Allow Photos access for OpenTrawl in System Settings.", control.SetupActionNone, nil)
	case "authorized":
		return control.NewSetupRequirement("photos_access_authorized", control.SetupKindPhotosPermission, control.SetupStateReady, "Photos access is allowed.", control.SetupActionNone, nil)
	case "limited":
		return control.NewSetupRequirement("photos_access_limited", control.SetupKindPhotosPermission, control.SetupStateNeedsAction, "Choose Full Access for OpenTrawl in System Settings.", control.SetupActionNone, nil)
	default:
		return control.NewSetupRequirement("photos_access_unavailable", control.SetupKindPhotosPermission, control.SetupStateUnavailable, "Photos access could not be checked.", control.SetupActionNone, nil)
	}
}
