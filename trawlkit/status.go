package trawlkit

type SyncReport struct {
	Added    int64    `json:"added"`
	Updated  int64    `json:"updated"`
	Removed  int64    `json:"removed"`
	Warnings []string `json:"warnings,omitempty"`
}

type Progress struct {
	Phase   string `json:"phase"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
}
