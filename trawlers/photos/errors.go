package photoscrawl

import "github.com/openclaw/crawlkit/output"

type commandError struct {
	Code    string
	Message string
	Remedy  string
}

func (e commandError) Error() string {
	return e.Message
}

func (e commandError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{Code: e.Code, Message: e.Message, Remedy: e.Remedy}
}
