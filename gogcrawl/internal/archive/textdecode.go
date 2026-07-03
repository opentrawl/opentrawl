package archive

import (
	"fmt"
	"io"
	"mime"
	"strings"
	"unicode/utf8"
)

var headerDecoder = mime.WordDecoder{CharsetReader: charsetReader}

func decodeTextPart(data []byte, charset string) string {
	if decoded, ok := decodeDeclaredText(data, charset); ok {
		return decoded
	}
	return decodeBestEffortText(data)
}

func decodeHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	decoded, err := headerDecoder.DecodeHeader(value)
	if err != nil {
		decoded = value
	}
	return strings.TrimSpace(decodeBestEffortText([]byte(decoded)))
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	data, err := io.ReadAll(input)
	if err != nil {
		return nil, err
	}
	decoded, ok := decodeDeclaredText(data, charset)
	if !ok {
		return nil, fmt.Errorf("unsupported charset %q", charset)
	}
	return strings.NewReader(decoded), nil
}

func decodeDeclaredText(data []byte, charset string) (string, bool) {
	switch canonicalCharset(charset) {
	case "":
		return decodeBestEffortText(data), true
	case "utf-8":
		if utf8.Valid(data) {
			return string(data), true
		}
		return decodeWindows1252(data), true
	case "us-ascii":
		if isASCII(data) {
			return string(data), true
		}
		return decodeWindows1252(data), true
	case "iso-8859-1":
		return decodeLatin1(data), true
	case "windows-1252":
		return decodeWindows1252(data), true
	default:
		return "", false
	}
}

func canonicalCharset(value string) string {
	value = strings.ToLower(strings.Trim(value, " \t\r\n\"'"))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "":
		return ""
	case "utf-8", "utf8":
		return "utf-8"
	case "us-ascii", "ascii", "ansi-x3.4-1968":
		return "us-ascii"
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "l1", "ibm819", "cp819":
		return "iso-8859-1"
	case "windows-1252", "windows1252", "cp1252", "cp-1252", "ms-ansi", "x-cp1252":
		return "windows-1252"
	default:
		return value
	}
}

func decodeBestEffortText(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	return decodeWindows1252(data)
}

func isASCII(data []byte) bool {
	for _, b := range data {
		if b > 0x7f {
			return false
		}
	}
	return true
}

func decodeLatin1(data []byte) string {
	var out strings.Builder
	out.Grow(len(data))
	for _, b := range data {
		if b < utf8.RuneSelf {
			out.WriteByte(b)
			continue
		}
		out.WriteRune(rune(b))
	}
	return out.String()
}

func decodeWindows1252(data []byte) string {
	var out strings.Builder
	out.Grow(len(data))
	for _, b := range data {
		switch {
		case b < utf8.RuneSelf:
			out.WriteByte(b)
		case b >= 0x80 && b <= 0x9f:
			out.WriteRune(windows1252C1[b-0x80])
		default:
			out.WriteRune(rune(b))
		}
	}
	return out.String()
}

var windows1252C1 = [...]rune{
	0x20ac, 0x0081, 0x201a, 0x0192, 0x201e, 0x2026, 0x2020, 0x2021,
	0x02c6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008d, 0x017d, 0x008f,
	0x0090, 0x2018, 0x2019, 0x201c, 0x201d, 0x2022, 0x2013, 0x2014,
	0x02dc, 0x2122, 0x0161, 0x203a, 0x0153, 0x009d, 0x017e, 0x0178,
}
