package trawlkit

import "github.com/opentrawl/opentrawl/trawlkit/output"

type ConfigFieldError struct {
	Field string
	Err   error
}

func (e ConfigFieldError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Field != "" {
		return "invalid config field " + e.Field
	}
	return "invalid config"
}

func (e ConfigFieldError) Unwrap() error {
	return e.Err
}

func (e ConfigFieldError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    "config_invalid",
		Message: e.Error(),
	}
}
