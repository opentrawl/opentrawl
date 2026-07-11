//go:build darwin

package photos

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func inspectDevelopmentVolume(path string) (developmentVolume, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return developmentVolume{}, err
	}
	mountPoint := int8String(stat.Mntonname[:])
	if mountPoint == "" {
		return developmentVolume{}, errors.New("development cache path has no mount point")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "/usr/sbin/diskutil", "info", "-plist", mountPoint).Output()
	if err != nil {
		return developmentVolume{}, fmt.Errorf("read mounted volume facts: %w", err)
	}
	stringsByKey, boolsByKey, err := parseDiskutilPlist(data)
	if err != nil {
		return developmentVolume{}, err
	}
	return volumeFromDiskutilFacts(mountPoint, stringsByKey, boolsByKey), nil
}

func volumeFromDiskutilFacts(mountPoint string, stringsByKey map[string]string, boolsByKey map[string]bool) developmentVolume {
	deviceNode := stringsByKey["DeviceNode"]
	deviceTreePath := stringsByKey["DeviceTreePath"]
	busProtocol := stringsByKey["BusProtocol"]
	reportedMount := stringsByKey["MountPoint"]
	local := strings.HasPrefix(deviceNode, "/dev/") && busProtocol != "" && !strings.EqualFold(busProtocol, "Disk Image")
	physical := strings.HasPrefix(deviceTreePath, "IODeviceTree:") && !boolsByKey["SystemImage"]
	return developmentVolume{
		MountPoint: reportedMount,
		Mounted:    reportedMount == mountPoint,
		Local:      local,
		External:   !boolsByKey["Internal"] && boolsByKey["RemovableMediaOrExternalDevice"],
		Physical:   physical,
		Writable:   boolsByKey["WritableVolume"],
	}
}

func parseDiskutilPlist(data []byte) (map[string]string, map[string]bool, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("read diskutil property list: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if ok && start.Name.Local == "dict" {
			return parsePlistDictionary(decoder)
		}
	}
}

func parsePlistDictionary(decoder *xml.Decoder) (map[string]string, map[string]bool, error) {
	stringsByKey := map[string]string{}
	boolsByKey := map[string]bool{}
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("read diskutil dictionary: %w", err)
		}
		switch value := token.(type) {
		case xml.EndElement:
			if value.Name.Local == "dict" {
				return stringsByKey, boolsByKey, nil
			}
		case xml.StartElement:
			if value.Name.Local != "key" {
				if err := decoder.Skip(); err != nil {
					return nil, nil, err
				}
				continue
			}
			var key string
			if err := decoder.DecodeElement(&key, &value); err != nil {
				return nil, nil, err
			}
			start, err := nextPlistStart(decoder)
			if err != nil {
				return nil, nil, err
			}
			switch start.Name.Local {
			case "string":
				var text string
				if err := decoder.DecodeElement(&text, &start); err != nil {
					return nil, nil, err
				}
				stringsByKey[key] = text
			case "true":
				boolsByKey[key] = true
				if err := decoder.Skip(); err != nil {
					return nil, nil, err
				}
			case "false":
				boolsByKey[key] = false
				if err := decoder.Skip(); err != nil {
					return nil, nil, err
				}
			default:
				if err := decoder.Skip(); err != nil {
					return nil, nil, err
				}
			}
		}
	}
}

func nextPlistStart(decoder *xml.Decoder) (xml.StartElement, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if start, ok := token.(xml.StartElement); ok {
			return start, nil
		}
	}
}

func int8String(value []int8) string {
	bytes := make([]byte, 0, len(value))
	for _, char := range value {
		if char == 0 {
			break
		}
		bytes = append(bytes, byte(char))
	}
	return string(bytes)
}
