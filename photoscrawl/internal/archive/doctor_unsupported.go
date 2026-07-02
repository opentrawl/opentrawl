//go:build !darwin

package archive

func sourceStoreChecks(string) []DoctorCheck {
	return []DoctorCheck{
		{ID: "source_store", State: "unsupported", Message: "not supported on this platform"},
		{ID: "full_disk_access", State: "unsupported", Message: "not supported on this platform"},
	}
}
