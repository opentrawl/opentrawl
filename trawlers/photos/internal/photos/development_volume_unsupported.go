//go:build !darwin

package photos

import "errors"

func inspectDevelopmentVolume(string) (developmentVolume, error) {
	return developmentVolume{}, errors.New("external development cache volume inspection requires macOS")
}
