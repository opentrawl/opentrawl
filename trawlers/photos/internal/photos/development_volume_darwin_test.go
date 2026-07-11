//go:build darwin

package photos

import "testing"

func TestParseDiskutilPlistRecognisesLocalExternalPhysicalVolume(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>MountPoint</key><string>/Volumes/Synthetic External</string>
<key>DeviceNode</key><string>/dev/disk42s1</string>
<key>DeviceTreePath</key><string>IODeviceTree:/synthetic/external-controller</string>
<key>BusProtocol</key><string>USB</string>
<key>Internal</key><false/>
<key>RemovableMediaOrExternalDevice</key><true/>
<key>SystemImage</key><false/>
<key>WritableVolume</key><true/>
<key>NestedIgnoredValue</key><array><dict><key>Internal</key><true/></dict></array>
</dict></plist>`)
	stringsByKey, boolsByKey, err := parseDiskutilPlist(data)
	if err != nil {
		t.Fatal(err)
	}
	volume := volumeFromDiskutilFacts("/Volumes/Synthetic External", stringsByKey, boolsByKey)
	t.Logf("boundary volume_plist input=%q output=%#v", data, volume)
	if !volume.Mounted || !volume.Local || !volume.External || !volume.Physical || !volume.Writable {
		t.Fatalf("external volume facts = %#v", volume)
	}
}

func TestVolumeFromDiskutilFactsRejectsInternalNetworkAndDiskImageMedia(t *testing.T) {
	tests := map[string]struct {
		strings map[string]string
		bools   map[string]bool
		field   func(developmentVolume) bool
	}{
		"secondary internal APFS": {
			strings: syntheticDiskutilStrings("PCI-Express", "IODeviceTree:/synthetic/internal-controller", "/dev/disk50s1"),
			bools:   map[string]bool{"Internal": true, "RemovableMediaOrExternalDevice": false, "WritableVolume": true},
			field:   func(volume developmentVolume) bool { return volume.External },
		},
		"network mount": {
			strings: syntheticDiskutilStrings("", "", ""),
			bools:   map[string]bool{"Internal": false, "RemovableMediaOrExternalDevice": true, "WritableVolume": true},
			field:   func(volume developmentVolume) bool { return volume.Local },
		},
		"disk image": {
			strings: syntheticDiskutilStrings("Disk Image", "IOService:/synthetic/AppleDiskImages2", "/dev/disk60s1"),
			bools:   map[string]bool{"Internal": false, "RemovableMediaOrExternalDevice": true, "SystemImage": true, "WritableVolume": true},
			field:   func(volume developmentVolume) bool { return volume.Physical },
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			volume := volumeFromDiskutilFacts("/Volumes/Synthetic", test.strings, test.bools)
			t.Logf("boundary unsafe_volume input_strings=%#v input_bools=%#v output=%#v", test.strings, test.bools, volume)
			if test.field(volume) {
				t.Fatalf("unsafe volume accepted: %#v", volume)
			}
		})
	}
}

func syntheticDiskutilStrings(bus, deviceTree, deviceNode string) map[string]string {
	return map[string]string{
		"MountPoint":     "/Volumes/Synthetic",
		"BusProtocol":    bus,
		"DeviceTreePath": deviceTree,
		"DeviceNode":     deviceNode,
	}
}
