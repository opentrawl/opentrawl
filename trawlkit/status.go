package trawlkit

type Progress struct {
	Phase   string `json:"phase"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
}
