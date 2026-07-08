package crawlkit

import "github.com/openclaw/crawlkit/output"

type ConfigValidator interface {
	Validate() error
}

type ConfigFieldError struct {
	Field string
	Fix   string
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

func (e ConfigFieldError) ErrorBody() output.ErrorBody {
	body := output.ErrorBody{
		Code:    "config_invalid",
		Message: e.Error(),
		Remedy:  e.Fix,
	}
	if e.Field != "" {
		body.Fields = map[string]any{"field": e.Field}
	}
	return body
}
