package cli

import (
	"strings"

	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
)

func sourceAppSyncLivenessForManifest(manifest *federationcontract.RegisteredTrawlerManifest) status.SourceAppSyncLiveness {
	if manifest.GetSourceFreshnessDependency() != federationcontract.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_SOURCE_APP_RUNNING {
		return status.SourceAppSyncLiveness_SOURCE_APP_SYNC_LIVENESS_NOT_APPLICABLE
	}
	bundleIdentifier := strings.TrimSpace(manifest.GetTrawlerBranding().GetBundleIdentifier())
	if bundleIdentifier == "" {
		return status.SourceAppSyncLiveness_SOURCE_APP_SYNC_LIVENESS_NOT_APPLICABLE
	}
	if sourceAppBundleIsRunning(bundleIdentifier) {
		return status.SourceAppSyncLiveness_SOURCE_APP_SYNC_LIVENESS_SOURCE_APP_RUNNING
	}
	return status.SourceAppSyncLiveness_SOURCE_APP_SYNC_LIVENESS_SOURCE_APP_NOT_RUNNING
}
