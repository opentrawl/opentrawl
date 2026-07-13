package photos

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type LocalMediaCandidate struct {
	Path  string `json:"path"`
	Class string `json:"class"`
	Size  int64  `json:"size"`
}

type LocalMediaIndex map[string][]LocalMediaCandidate

func BuildLocalMediaIndex(libraryPath string) (LocalMediaIndex, error) {
	return localMediaIndex(libraryPath)
}

func (index LocalMediaIndex) Candidates(localIdentifier string) []LocalMediaCandidate {
	return append([]LocalMediaCandidate(nil), index[mediaUUID(localIdentifier)]...)
}

// AttachLocalMediaPaths finds already-local Photos package media without asking
// Photos or iCloud to export/download anything.
func AttachLocalMediaPaths(snapshot *LibrarySnapshot, libraryPath string) error {
	if snapshot == nil || strings.TrimSpace(libraryPath) == "" {
		return nil
	}
	index, err := localMediaIndex(libraryPath)
	if err != nil {
		return err
	}
	if len(index) == 0 {
		return nil
	}
	for assetIndex := range snapshot.Assets {
		asset := &snapshot.Assets[assetIndex]
		uuid := mediaUUID(asset.LocalIdentifier)
		if uuid == "" {
			continue
		}
		candidate, ok := UniquePackageOriginal(index[uuid])
		if !ok {
			continue
		}
		alreadyAttached := false
		for _, resource := range asset.Resources {
			if resource.LocalPath == candidate.Path {
				alreadyAttached = true
				break
			}
		}
		if alreadyAttached || asset.MediaType != "image" {
			continue
		}
		asset.Resources = append(asset.Resources, Resource{
			Type:             "local_original",
			UTI:              utiForPath(candidate.Path),
			OriginalFilename: filepath.Base(candidate.Path),
			LocalPath:        candidate.Path,
			Availability:     "local",
			FileSize:         candidate.Size,
			AvailableLocally: true,
			NeedsDownload:    false,
			Metadata: map[string]any{
				"local_path_class":  "original",
				"local_path_source": "photos_library_package",
			},
		})
	}
	return nil
}

// UniquePackageOriginal returns a package original only when the UUID maps to
// one path. The resolver inspects the real file instead of trusting the
// archive's recorded size. Ambiguous matches fall through to PhotoKit.
func UniquePackageOriginal(candidates []LocalMediaCandidate) (LocalMediaCandidate, bool) {
	var original LocalMediaCandidate
	for _, candidate := range candidates {
		if candidate.Class != "original" || strings.TrimSpace(candidate.Path) == "" {
			continue
		}
		if original.Path != "" {
			return LocalMediaCandidate{}, false
		}
		original = candidate
	}
	return original, original.Path != ""
}

func localMediaIndex(libraryPath string) (LocalMediaIndex, error) {
	root := filepath.Join(libraryPath, "originals")
	out := LocalMediaIndex{}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !classifiablePath(path) {
			return nil
		}
		uuid := mediaUUID(filepath.Base(path))
		if uuid == "" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		out[uuid] = append(out[uuid], LocalMediaCandidate{
			Path:  path,
			Class: "original",
			Size:  info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func classifiablePath(path string) bool {
	return IsOriginalExtension(filepath.Ext(path))
}

func mediaUUID(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) < 36 {
		return ""
	}
	for start := 0; start <= len(value)-36; start++ {
		candidate := value[start : start+36]
		if isUUID(candidate) {
			return candidate
		}
	}
	return ""
}

// CanonicalAssetUUID returns the UUID identity shared by Photos.sqlite and a
// PhotoKit local identifier. It returns an empty string when no UUID is present.
func CanonicalAssetUUID(value string) string { return mediaUUID(value) }

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'A' || r > 'F') {
				return false
			}
		}
	}
	return true
}

func utiForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "public.jpeg"
	case ".png":
		return "public.png"
	case ".heic":
		return "public.heic"
	default:
		return "public.image"
	}
}
