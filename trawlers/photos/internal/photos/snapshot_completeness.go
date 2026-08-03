package photos

import "fmt"

type SnapshotCompletenessState string

const (
	SnapshotComplete  SnapshotCompletenessState = "complete"
	SnapshotPartial   SnapshotCompletenessState = "partial"
	SnapshotLimited   SnapshotCompletenessState = "limited"
	SnapshotFailed    SnapshotCompletenessState = "failed"
	SnapshotCancelled SnapshotCompletenessState = "cancelled"
)

type SnapshotCompleteness struct {
	State                            SnapshotCompletenessState
	DatabaseCopyCompleted            bool
	ResourceQueriesCompleted         bool
	AlbumQueriesCompleted            bool
	AssetQueryCompleted              bool
	ActiveAssetCount                 int
	UniqueActiveAssetIdentifierCount int
}

func (completeness SnapshotCompleteness) Validate() error {
	switch completeness.State {
	case SnapshotComplete, SnapshotPartial, SnapshotLimited, SnapshotFailed, SnapshotCancelled:
	default:
		return fmt.Errorf("unsupported snapshot completeness state %q", completeness.State)
	}
	if completeness.ActiveAssetCount < 0 || completeness.UniqueActiveAssetIdentifierCount < 0 {
		return fmt.Errorf("snapshot completeness counts must not be negative")
	}
	if completeness.State == SnapshotComplete && (!completeness.DatabaseCopyCompleted || !completeness.ResourceQueriesCompleted || !completeness.AlbumQueriesCompleted || !completeness.AssetQueryCompleted) {
		return fmt.Errorf("complete Photos snapshot is missing completed source phases")
	}
	return nil
}

func (completeness SnapshotCompleteness) Complete() bool {
	return completeness.State == SnapshotComplete
}
