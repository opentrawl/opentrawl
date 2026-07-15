package archive

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	repoPrompts "github.com/opentrawl/opentrawl/trawlers/photos/prompts"
	"github.com/opentrawl/opentrawl/trawlkit/model"
)

const (
	modelClassifierSource = "photo_card"
	modelPromptVersion    = repoPrompts.PhotoCardVersion
	modelParserVersion    = "photo-card-tool.v2"
)

type modelClassifier struct {
	modelID       string
	promptVersion string
	baseURL       string
	client        *model.Client
}

func newModelClassifier(modelID, baseURL, bearerKeyEnv string) (modelClassifier, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(baseURL, "/api") {
		baseURL = strings.TrimSuffix(baseURL, "/api")
	}
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	client, err := model.New(model.Config{
		BaseURL:      baseURL,
		Model:        modelID,
		BearerKeyEnv: bearerKeyEnv,
	})
	if err != nil {
		return modelClassifier{}, err
	}
	return modelClassifier{
		modelID:       strings.TrimSpace(modelID),
		promptVersion: modelPromptVersion,
		baseURL:       baseURL,
		client:        client,
	}, nil
}

// imageMeta identifies the visual image sent to the model. The immutable
// original has separate metadata provenance and need not be the same image.
type imageMeta struct {
	Bytes  int64
	SHA256 string
}

func (c modelClassifier) parseResult(response model.Response, prepared preparedCardRequest) (modelResult, error) {
	card, err := parsePhotoCardToolCall(response.ToolCalls, prepared)
	if err != nil {
		return modelResult{}, err
	}
	return modelResult{
		Payload:     photoCardPayload(card),
		ImageBytes:  prepared.Image.Bytes,
		ImageSHA256: prepared.Image.SHA256,
		Card:        card,
		TypedCard:   photoCardMessage(card),
		Observations: observationsFromCard(card, prepared),
	}, nil
}

// captureLocation resolves the timezone Photos stored for an asset. Apple
// records fixed offsets as "GMT-0700"-style names, which time.LoadLocation
// rejects; they are real timezones, not absence. Unknown means nil — the
// caller must then render UTC, never the reviewing machine's timezone.
func captureLocation(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	rest := ""
	switch {
	case strings.HasPrefix(name, "GMT"):
		rest = name[3:]
	case strings.HasPrefix(name, "UTC"):
		rest = name[3:]
	default:
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
		return nil
	}
	if rest == "" {
		// Bare "GMT"/"UTC" is a real recorded zone at offset zero.
		return time.FixedZone(name, 0)
	}
	if len(rest) != 5 || (rest[0] != '+' && rest[0] != '-') {
		return nil
	}
	hours, errH := strconv.Atoi(rest[1:3])
	minutes, errM := strconv.Atoi(rest[3:5])
	if errH != nil || errM != nil || hours > 14 || minutes > 59 {
		return nil
	}
	offset := hours*3600 + minutes*60
	if rest[0] == '-' {
		offset = -offset
	}
	return time.FixedZone(name, offset)
}

// localCaptureTime renders a capture instant in the asset's own timezone.
// When the timezone is unknown it renders UTC: the machine's timezone is a
// fact about the reviewer, not the photo.
func localCaptureTime(value, timezoneName string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	if loc := captureLocation(timezoneName); loc != nil {
		return parsed.In(loc).Format(time.RFC3339)
	}
	return parsed.UTC().Format(time.RFC3339)
}

// splitSubtypes turns Photos' numeric kind subtypes into words a reader (and
// the model) can use; unknown codes carry no meaning and are dropped.
func splitSubtypes(value string) []string {
	names := map[string]string{
		"kind_subtype:1":   "panorama",
		"kind_subtype:2":   "live_photo",
		"kind_subtype:10":  "screenshot",
		"kind_subtype:100": "video_streamed",
		"kind_subtype:101": "time_lapse",
		"kind_subtype:102": "slow_motion",
	}
	out := []string{}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		part = strings.TrimSpace(part)
		if name, ok := names[part]; ok {
			out = append(out, name)
		}
	}
	return out
}

func (resource classifyResource) Availability() string {
	if resource.AvailableLocally && !resource.NeedsDownload {
		return "local"
	}
	if resource.NeedsDownload {
		return "in_icloud"
	}
	return ""
}

func (c modelClassifier) remote() bool {
	parsed, err := url.Parse(strings.TrimSpace(c.baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

// knownPlaceRelationship phrases the match for the model in plain words —
// one sentence fragment, no kind enums.
func knownPlaceRelationship(match KnownPlaceMatch) string {
	switch {
	case match.After && match.Kind == KnownPlaceKindWork:
		return "the user's former workplace"
	case match.After:
		return "the user's former home"
	case match.Kind == KnownPlaceKindWork:
		return "the user's workplace"
	case match.Kind == KnownPlaceKindFormerHome:
		return "the user's home at the time this photo was taken"
	default:
		return "the user's home"
	}
}
