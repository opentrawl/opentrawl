package prompts

import _ "embed"

const (
	PhotoCardVersion     = "photo-card-v3.2"
	DefaultPhotoCardPath = "prompts/photo-card-v3.md"
)

//go:embed photo-card-v3.md
var PhotoCardV3 string
