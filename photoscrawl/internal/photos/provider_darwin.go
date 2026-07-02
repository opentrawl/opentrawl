//go:build darwin

package photos

func NewProvider() Provider {
	return FallbackProvider{
		Primary:   PhotoKitProvider{},
		Secondary: SQLiteSnapshotProvider{},
	}
}
