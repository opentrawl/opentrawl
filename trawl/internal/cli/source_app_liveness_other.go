//go:build !darwin

package cli

func sourceAppBundleIsRunning(string) bool {
	return false
}
