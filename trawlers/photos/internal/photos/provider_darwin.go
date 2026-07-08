//go:build darwin

package photos

func NewProvider() Provider {
	return SQLiteSnapshotProvider{}
}
