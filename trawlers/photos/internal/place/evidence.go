package place

import "encoding/json"

const (
	evidenceParserVersion = "photos-place-evidence-v3"
	evidenceStateComplete = "complete"
	appleEvidenceProvider = "apple"
	maxRawEvidenceBytes   = 4 << 20
)

// EvidenceRecord is the checked provider record consumed by card creation.
// The public product reopens these records but never produces them.
type EvidenceRecord struct {
	Input                Input               `json:"input"`
	ProviderIdentity     string              `json:"provider_identity"`
	Operation            string              `json:"operation"`
	SelectionPolicy      SelectionPolicy     `json:"selection_policy"`
	CoordinateVariant    string              `json:"coordinate_variant"`
	ParserVersion        string              `json:"parser_version"`
	PreAuthRequestFile   string              `json:"pre_auth_request_file"`
	PreAuthRequestSHA256 string              `json:"pre_auth_request_sha256"`
	RawResponseFile      string              `json:"raw_response_file"`
	RawResponseSHA256    string              `json:"raw_response_sha256"`
	RawHeadersFile       string              `json:"raw_headers_file,omitempty"`
	RawHeadersSHA256     string              `json:"raw_headers_sha256,omitempty"`
	HTTPStatus           int                 `json:"http_status,omitempty"`
	Address              *Address            `json:"address,omitempty"`
	Candidates           []EvidenceCandidate `json:"candidates"`
	CompletionState      string              `json:"completion_state"`
	StopReason           string              `json:"stop_reason,omitempty"`
	StopDetail           string              `json:"stop_detail,omitempty"`
	ProviderErrorClass   string              `json:"provider_error_class,omitempty"`
	CacheIdentity        string              `json:"cache_identity"`
	Cached               bool                `json:"cached,omitempty"`
	RecordDir            string              `json:"record_dir,omitempty"`
	CredentialReference  string              `json:"credential_reference,omitempty"`
	StartedAt            string              `json:"started_at"`
	CompletedAt          string              `json:"completed_at"`
	DurationMilliseconds float64             `json:"duration_milliseconds"`
}

// SelectionPolicy preserves why a bounded response is complete. A producer
// may mark only an explicit reverse selection as complete at its limit.
// Search and nearby operations remain incomplete when they reach their limit.
type SelectionPolicy struct {
	RequestedLimit          int  `json:"requested_limit"`
	LimitReached            bool `json:"limit_reached"`
	MoreResultsNotRequested bool `json:"more_results_not_requested"`
	BoundedReverse          bool `json:"bounded_reverse"`
}

// CheckedOperation names one exact checked-cache boundary that may enter a
// card. Its order is the card's evidence order.
type CheckedOperation struct {
	ProviderIdentity    string                `json:"provider_identity"`
	Operation           string                `json:"operation"`
	CoordinateVariant   string                `json:"coordinate_variant"`
	CredentialReference string                `json:"credential_reference,omitempty"`
	SelectionPolicy     SelectionPolicy       `json:"selection_policy"`
	Parser              CheckedEvidenceParser `json:"-"`
}

// CheckedEvidenceParser reproduces the structured evidence from one cached
// raw response. The loader uses it to reject altered record fields.
type CheckedEvidenceParser func(raw []byte, status int, input Input) (*Address, []EvidenceCandidate, error)

type EvidenceCandidate struct {
	ProviderIndex  int             `json:"provider_index"`
	ProviderID     string          `json:"provider_id,omitempty"`
	Name           string          `json:"name,omitempty"`
	Categories     []string        `json:"categories"`
	Coordinate     *Coordinate     `json:"coordinate,omitempty"`
	DistanceM      float64         `json:"distance_m,omitempty"`
	Address        *Address        `json:"address,omitempty"`
	Source         string          `json:"source,omitempty"`
	ProviderResult json.RawMessage `json:"provider_result,omitempty"`
}
