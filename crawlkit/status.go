package crawlkit

type Doctor struct {
	Checks []Check `json:"checks"`
}

type Check struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
	Remedy  string `json:"remedy,omitempty"`
}

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
