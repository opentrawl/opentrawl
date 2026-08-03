package archive

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type RetainedSourceSnapshot struct {
	ConfiguredLibraryPath              string
	Provider                           string
	StartedAt                          string
	CompletedAt                        string
	ExpectedActiveAssetCount           int64
	ExpectedUniqueAssetIdentifierCount int64
	DatabaseSnapshotFileCount          int64
	DatabaseSnapshotBytes              int64
	AssetCount                         int64
	ResourceCount                      int64
	AlbumMembershipCount               int64
	LocationCount                      int64
	CompletenessState                  string
	DatabaseCopyCompleted              bool
	ResourceQueriesCompleted           bool
	AlbumQueriesCompleted              bool
	AssetQueryCompleted                bool
}

func LoadLatestRetainedSourceSnapshot(ctx context.Context, openedStore *store.Store) (RetainedSourceSnapshot, error) {
	if err := validateReadStore(ctx, openedStore); err != nil {
		return RetainedSourceSnapshot{}, err
	}
	var snapshot RetainedSourceSnapshot
	err := openedStore.DB().QueryRowContext(ctx, `
select source_library.configured_library_path,
       crawl_snapshot.provider,
       crawl_snapshot.started_at,
       crawl_snapshot.completed_at,
       crawl_snapshot.expected_active_asset_count,
       crawl_snapshot.expected_unique_asset_identifier_count,
       crawl_snapshot.database_snapshot_file_count,
       crawl_snapshot.database_snapshot_bytes,
       crawl_snapshot.asset_count,
       crawl_snapshot.resource_count,
       crawl_snapshot.album_membership_count,
       crawl_snapshot.location_count,
       crawl_snapshot.completeness_state,
       crawl_snapshot.database_copy_completed,
       crawl_snapshot.resource_queries_completed,
       crawl_snapshot.album_queries_completed,
       crawl_snapshot.asset_query_completed
from crawl_snapshot
join source_library on source_library.id = crawl_snapshot.source_library_id
order by crawl_snapshot.completed_at desc
limit 1`).Scan(
		&snapshot.ConfiguredLibraryPath,
		&snapshot.Provider,
		&snapshot.StartedAt,
		&snapshot.CompletedAt,
		&snapshot.ExpectedActiveAssetCount,
		&snapshot.ExpectedUniqueAssetIdentifierCount,
		&snapshot.DatabaseSnapshotFileCount,
		&snapshot.DatabaseSnapshotBytes,
		&snapshot.AssetCount,
		&snapshot.ResourceCount,
		&snapshot.AlbumMembershipCount,
		&snapshot.LocationCount,
		&snapshot.CompletenessState,
		&snapshot.DatabaseCopyCompleted,
		&snapshot.ResourceQueriesCompleted,
		&snapshot.AlbumQueriesCompleted,
		&snapshot.AssetQueryCompleted,
	)
	return snapshot, err
}
