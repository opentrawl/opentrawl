package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	cklog "github.com/openclaw/crawlkit/log"
)

type Format string

const (
	Text Format = "text"
	JSON Format = "json"
	Log  Format = "log"
)

type UsageError struct {
	Err error
}

func (e UsageError) Error() string {
	if e.Err == nil {
		return "usage error"
	}
	return e.Err.Error()
}

func (e UsageError) Unwrap() error {
	return e.Err
}

type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Remedy  string         `json:"remedy"`
	Fields  map[string]any `json:"-"`
}

func (e ErrorBody) MarshalJSON() ([]byte, error) {
	body := map[string]any{
		"code":    firstNonEmpty(e.Code, "command_failed"),
		"message": firstNonEmpty(e.Message, "command failed"),
		"remedy":  e.Remedy,
	}
	for key, value := range e.Fields {
		if strings.TrimSpace(key) == "" || emptyErrorField(value) {
			continue
		}
		body[key] = value
	}
	return json.Marshal(body)
}

type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

func WriteError(w io.Writer, body ErrorBody) error {
	enc := json.NewEncoder(w)
	return enc.Encode(ErrorEnvelope{Error: body})
}

type ErrorBodyProvider interface {
	ErrorBody() ErrorBody
}

type RenderedError struct {
	Err error
}

func (e *RenderedError) Error() string {
	if e.Err == nil {
		return "rendered error"
	}
	return e.Err.Error()
}

func (e *RenderedError) Unwrap() error {
	return e.Err
}

func Rendered(err error) error {
	if err == nil {
		return nil
	}
	return &RenderedError{Err: err}
}

func IsRendered(err error) bool {
	var rendered *RenderedError
	return errors.As(err, &rendered)
}

func WriteJSONErrorIfNeeded(w io.Writer, jsonOut bool, err error) error {
	if err == nil || !jsonOut || IsRendered(err) {
		return err
	}
	body := ErrorBody{
		Code:    "command_failed",
		Message: err.Error(),
	}
	var bodyErr ErrorBodyProvider
	if errors.As(err, &bodyErr) {
		body = bodyErr.ErrorBody()
	} else {
		var world cklog.WorldMustChange
		if errors.As(err, &world) {
			body.Message = world.Error()
			body.Remedy = world.Remedy
		}
	}
	body.Code = firstNonEmpty(body.Code, "command_failed")
	body.Message = firstNonEmpty(body.Message, err.Error(), "command failed")
	_ = WriteError(w, body)
	return Rendered(err)
}

func Resolve(format string, jsonFlag bool) (Format, error) {
	if jsonFlag {
		return JSON, nil
	}
	switch Format(format) {
	case "", Text:
		return Text, nil
	case JSON:
		return JSON, nil
	case Log:
		return Log, nil
	default:
		return "", UsageError{Err: fmt.Errorf("unknown output format %q", format)}
	}
}

func Write(w io.Writer, format Format, label string, value any) error {
	switch format {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	case Log:
		if label == "" {
			label = "result"
		}
		if !validLogLabel(label) {
			return UsageError{Err: fmt.Errorf("invalid log label %q", label)}
		}
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s=%s\n", label, body)
		return err
	case "", Text:
		_, err := fmt.Fprintln(w, value)
		return err
	default:
		return UsageError{Err: fmt.Errorf("unknown output format %q", format)}
	}
}

func validLogLabel(label string) bool {
	for _, r := range label {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-' {
			continue
		}
		return false
	}
	return label != ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func emptyErrorField(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return true
	}
	switch rv.Kind() {
	case reflect.String:
		return strings.TrimSpace(rv.String()) == ""
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if rv.IsNil() {
			return true
		}
		if rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
			return emptyErrorField(rv.Elem().Interface())
		}
		return rv.Len() == 0
	case reflect.Array:
		return rv.Len() == 0
	case reflect.Bool:
		return !rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() == 0
	}
	return false
}

func IsUsage(err error) bool {
	var usage UsageError
	return errors.As(err, &usage)
}
