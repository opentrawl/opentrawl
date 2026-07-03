package prompts

import _ "embed"

const (
	PhotoCardVersion     = "photo-card-v2"
	DefaultPhotoCardPath = "prompts/photo-card-v2.md"
)

//go:embed photo-card-v2.md
var PhotoCardV2 string
