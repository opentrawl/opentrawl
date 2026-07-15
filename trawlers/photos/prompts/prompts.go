package prompts

import _ "embed"

const (
	PhotoCardVersion     = "photo-card-v5"
	DefaultPhotoCardPath = "prompts/photo-card-v5.md"
)

//go:embed photo-card-v5.md
var PhotoCardV5 string
