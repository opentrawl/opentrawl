package prompts

import _ "embed"

const (
	PhotoCardVersion                     = "photo-card-v2"
	LocalMultimodalObservationsV1Version = "photoscrawl.local-multimodal-observations.v1"
	DefaultPhotoCardPath                 = "prompts/photo-card-v2.md"
)

//go:embed local-multimodal-observations-v1.md
var LocalMultimodalObservationsV1 string
