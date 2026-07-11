package imagemetadata

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const ExtractorVersion = "imageio-v1"

type ValueType string

const (
	ValueNull       ValueType = "null"
	ValueString     ValueType = "string"
	ValueBoolean    ValueType = "boolean"
	ValueSigned     ValueType = "signed_integer"
	ValueUnsigned   ValueType = "unsigned_integer"
	ValueDecimal    ValueType = "decimal"
	ValueDate       ValueType = "date"
	ValueData       ValueType = "data"
	ValueArray      ValueType = "array"
	ValueDictionary ValueType = "dictionary"
)

// Value is ImageIO metadata with the Objective-C value type kept explicit.
// Integers and decimals use strings so JSON cannot round or overflow them.
type Value struct {
	Type       ValueType        `json:"type"`
	String     *string          `json:"string,omitempty"`
	Boolean    *bool            `json:"boolean,omitempty"`
	Signed     *string          `json:"signed_integer,omitempty"`
	Unsigned   *string          `json:"unsigned_integer,omitempty"`
	Decimal    *string          `json:"decimal,omitempty"`
	Date       *string          `json:"date,omitempty"`
	Data       *string          `json:"data,omitempty"`
	Array      []Value          `json:"array,omitempty"`
	Dictionary map[string]Value `json:"dictionary,omitempty"`
}

type Image struct {
	Index      int   `json:"index"`
	Properties Value `json:"properties"`
}

type Record struct {
	ExtractorVersion string  `json:"extractor_version"`
	OriginalSHA256   string  `json:"original_sha256"`
	Container        Value   `json:"container"`
	Images           []Image `json:"images"`
}

type Exclusion struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Projection struct {
	ExtractorVersion   string      `json:"extractor_version"`
	OriginalSHA256     string      `json:"original_sha256"`
	Lines              []string    `json:"lines"`
	Exclusions         []Exclusion `json:"exclusions"`
	RenderedFieldCount int         `json:"rendered_field_count"`
}

func (p Projection) Text() string {
	return strings.Join(p.Lines, "\n")
}

type Proof struct {
	ExtractorVersion string `json:"extractor_version"`
	OriginalSHA256   string `json:"original_sha256"`
	RecordSHA256     string `json:"record_sha256"`
	ProjectionSHA256 string `json:"projection_sha256"`
	FieldCount       int    `json:"field_count"`
	RenderedCount    int    `json:"rendered_count"`
	ExclusionCount   int    `json:"exclusion_count"`
}

type Artifacts struct {
	Record     Record
	Projection Projection
	Proof      Proof
	CacheHit   bool
}

func (v Value) MarshalJSON() ([]byte, error) {
	payload := map[string]any{"type": v.Type}
	switch v.Type {
	case ValueNull:
	case ValueString:
		payload["string"] = v.String
	case ValueBoolean:
		payload["boolean"] = v.Boolean
	case ValueSigned:
		payload["signed_integer"] = v.Signed
	case ValueUnsigned:
		payload["unsigned_integer"] = v.Unsigned
	case ValueDecimal:
		payload["decimal"] = v.Decimal
	case ValueDate:
		payload["date"] = v.Date
	case ValueData:
		payload["data"] = v.Data
	case ValueArray:
		payload["array"] = v.Array
	case ValueDictionary:
		payload["dictionary"] = v.Dictionary
	default:
		return nil, fmt.Errorf("unsupported value type %q", v.Type)
	}
	return json.Marshal(payload)
}

func (r Record) validate() error {
	if r.ExtractorVersion != ExtractorVersion {
		return fmt.Errorf("extractor version = %q, want %q", r.ExtractorVersion, ExtractorVersion)
	}
	if err := validateSHA256(r.OriginalSHA256); err != nil {
		return fmt.Errorf("original SHA-256: %w", err)
	}
	if err := r.Container.validate("container"); err != nil {
		return err
	}
	if len(r.Images) == 0 {
		return errors.New("ImageIO returned no image indexes")
	}
	for i, image := range r.Images {
		if image.Index != i {
			return fmt.Errorf("image index %d is stored at position %d", image.Index, i)
		}
		if err := image.Properties.validate(fmt.Sprintf("image[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

func (v Value) validate(path string) error {
	payloads := 0
	if v.String != nil {
		payloads++
	}
	if v.Boolean != nil {
		payloads++
	}
	if v.Signed != nil {
		payloads++
	}
	if v.Unsigned != nil {
		payloads++
	}
	if v.Decimal != nil {
		payloads++
	}
	if v.Date != nil {
		payloads++
	}
	if v.Data != nil {
		payloads++
	}
	if v.Array != nil {
		payloads++
	}
	if v.Dictionary != nil {
		payloads++
	}

	wantPayloads := 1
	if v.Type == ValueNull {
		wantPayloads = 0
	}
	if payloads != wantPayloads {
		return fmt.Errorf("%s has %d payloads for type %q", path, payloads, v.Type)
	}

	switch v.Type {
	case ValueNull:
	case ValueString:
		if v.String == nil {
			return fmt.Errorf("%s is missing its string", path)
		}
	case ValueBoolean:
		if v.Boolean == nil {
			return fmt.Errorf("%s is missing its boolean", path)
		}
	case ValueSigned:
		if v.Signed == nil {
			return fmt.Errorf("%s is missing its signed integer", path)
		}
		if _, err := strconv.ParseInt(*v.Signed, 10, 64); err != nil {
			return fmt.Errorf("%s signed integer: %w", path, err)
		}
	case ValueUnsigned:
		if v.Unsigned == nil {
			return fmt.Errorf("%s is missing its unsigned integer", path)
		}
		if _, err := strconv.ParseUint(*v.Unsigned, 10, 64); err != nil {
			return fmt.Errorf("%s unsigned integer: %w", path, err)
		}
	case ValueDecimal:
		if v.Decimal == nil {
			return fmt.Errorf("%s is missing its decimal", path)
		}
		decimal, err := strconv.ParseFloat(*v.Decimal, 64)
		if err != nil {
			return fmt.Errorf("%s decimal: %w", path, err)
		}
		if math.IsNaN(decimal) || math.IsInf(decimal, 0) {
			return fmt.Errorf("%s decimal is not finite", path)
		}
	case ValueDate:
		if v.Date == nil {
			return fmt.Errorf("%s is missing its date", path)
		}
		if _, err := time.Parse(time.RFC3339Nano, *v.Date); err != nil {
			return fmt.Errorf("%s date: %w", path, err)
		}
	case ValueData:
		if v.Data == nil {
			return fmt.Errorf("%s is missing its data", path)
		}
		if _, err := base64.StdEncoding.DecodeString(*v.Data); err != nil {
			return fmt.Errorf("%s data: %w", path, err)
		}
	case ValueArray:
		if v.Array == nil {
			return fmt.Errorf("%s is missing its array", path)
		}
		for i := range v.Array {
			if err := v.Array[i].validate(fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case ValueDictionary:
		if v.Dictionary == nil {
			return fmt.Errorf("%s is missing its dictionary", path)
		}
		for key, child := range v.Dictionary {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("%s has an empty dictionary key", path)
			}
			if err := child.validate(path + "." + key); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s has unsupported value type %q", path, v.Type)
	}
	return nil
}

func validateSHA256(value string) error {
	if len(value) != 64 {
		return errors.New("must contain 64 hexadecimal characters")
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return errors.New("must be lowercase hexadecimal")
		}
	}
	return nil
}
