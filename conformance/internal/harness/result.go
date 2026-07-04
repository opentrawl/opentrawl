package harness

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

const (
	CheckMetadata         = "metadata"
	CheckGrammar          = "grammar"
	CheckStatus           = "status"
	CheckDoctor           = "doctor"
	CheckSecrets          = "secrets scan"
	CheckReadsNeverMutate = "reads never mutate"
	CheckSearch           = "search"
	CheckOpen             = "open"
	CheckHumanReadable    = "human output"
)

type CheckResult struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Remedy string `json:"remedy,omitempty"`
}

type Report []CheckResult

func (r Report) HasFailures() bool {
	for _, result := range r {
		if result.Status == StatusFail {
			return true
		}
	}
	return false
}

func pass(name, detail string) CheckResult {
	return CheckResult{Name: name, Status: StatusPass, Detail: detail}
}

func warn(name, detail string) CheckResult {
	return CheckResult{Name: name, Status: StatusWarn, Detail: detail}
}

func fail(name, detail, remedy string) CheckResult {
	return CheckResult{Name: name, Status: StatusFail, Detail: detail, Remedy: remedy}
}
