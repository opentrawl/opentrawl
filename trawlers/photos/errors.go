package photos

import "github.com/opentrawl/opentrawl/trawlkit/output"

type commandError struct {
	Code    string
	Message string
}

func (e commandError) Error() string {
	return e.Message
}

func (e commandError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{Code: e.Code, Message: e.Message}
}
