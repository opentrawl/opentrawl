package mailparse

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/ianaindex"
)

var emailHeaderWordDecoder = mime.WordDecoder{CharsetReader: declaredCharsetReader}

// DecodeEmailHeaderText decodes RFC 2047 encoded words and undeclared 8-bit
// bytes in one header value, and joins folded lines with single spaces.
func DecodeEmailHeaderText(headerValue string) string {
	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return ""
	}
	decoded, err := emailHeaderWordDecoder.DecodeHeader(headerValue)
	if err != nil {
		decoded = headerValue
	}
	return strings.Join(strings.Fields(decodeUndeclaredEmailText([]byte(decoded))), " ")
}

func declaredCharsetReader(charsetLabel string, input io.Reader) (io.Reader, error) {
	data, err := io.ReadAll(input)
	if err != nil {
		return nil, err
	}
	declaredEncoding := lookUpDeclaredCharset(charsetLabel)
	if declaredEncoding == nil {
		return nil, fmt.Errorf("unsupported charset %q", charsetLabel)
	}
	return strings.NewReader(decodeWithDeclaredEncoding(data, declaredEncoding)), nil
}

func decodeEmailTextPart(data []byte, charsetLabel string) string {
	var text string
	if declaredEncoding := lookUpDeclaredCharset(charsetLabel); declaredEncoding != nil {
		text = decodeWithDeclaredEncoding(data, declaredEncoding)
	} else {
		text = decodeUndeclaredEmailText(data)
	}
	return decodeResidualQuotedPrintableText(text)
}

// lookUpDeclaredCharset accepts every IANA and WHATWG charset label. It
// returns nil for an empty or unknown label.
func lookUpDeclaredCharset(charsetLabel string) encoding.Encoding {
	charsetLabel = strings.ToLower(strings.Trim(charsetLabel, " \t\r\n\"'"))
	if charsetLabel == "" {
		return nil
	}
	if found, err := htmlindex.Get(charsetLabel); err == nil && found != nil {
		return found
	}
	if found, err := ianaindex.MIME.Encoding(charsetLabel); err == nil && found != nil {
		return found
	}
	return nil
}

// decodeWithDeclaredEncoding trusts valid UTF-8 when the sender declared
// UTF-8 or US-ASCII, the two labels senders most often get wrong.
func decodeWithDeclaredEncoding(data []byte, declaredEncoding encoding.Encoding) string {
	if declaredEncoding == encoding.Nop || isUTF8Encoding(declaredEncoding) || declaredEncoding == charmap.Windows1252 {
		if utf8.Valid(data) {
			return string(data)
		}
		return decodeWindows1252Text(data)
	}
	decoded, err := declaredEncoding.NewDecoder().Bytes(data)
	if err != nil {
		return decodeUndeclaredEmailText(data)
	}
	return string(decoded)
}

func isUTF8Encoding(candidate encoding.Encoding) bool {
	name, err := htmlindex.Name(candidate)
	return err == nil && name == "utf-8"
}

func decodeUndeclaredEmailText(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	return decodeWindows1252Text(data)
}

func decodeWindows1252Text(data []byte) string {
	decoded, err := charmap.Windows1252.NewDecoder().Bytes(data)
	if err != nil {
		return strings.ToValidUTF8(string(data), "\uFFFD")
	}
	return string(decoded)
}

// decodeResidualQuotedPrintableText handles the sender bug class where a
// part declares 7bit or US-ASCII but carries quoted-printable escapes. It
// runs after charset conversion, so it keeps the original unless the decoded
// result is valid UTF-8 with fewer escapes.
func decodeResidualQuotedPrintableText(text string) string {
	data := []byte(text)
	hexEscapes, distinctHexEscapes := quotedPrintableHexEscapeCounts(data)
	hasSoftLineBreak := bytes.Contains(data, []byte("=\r\n")) || bytes.Contains(data, []byte("=\n"))
	if !hasSoftLineBreak && distinctHexEscapes < 3 {
		return text
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
	if err != nil || !utf8.Valid(decoded) {
		return text
	}
	if decodedHexEscapes, _ := quotedPrintableHexEscapeCounts(decoded); decodedHexEscapes >= hexEscapes {
		return text
	}
	return string(decoded)
}

func quotedPrintableHexEscapeCounts(data []byte) (int, int) {
	var seen [256]bool
	total, distinct := 0, 0
	for index := 0; index+2 < len(data); index++ {
		if data[index] != '=' || !isHexDigit(data[index+1]) || !isHexDigit(data[index+2]) {
			continue
		}
		total++
		value := hexDigitValue(data[index+1])<<4 | hexDigitValue(data[index+2])
		if !seen[value] {
			seen[value] = true
			distinct++
		}
	}
	return total, distinct
}

func isHexDigit(character byte) bool {
	return (character >= '0' && character <= '9') || (character >= 'A' && character <= 'F') || (character >= 'a' && character <= 'f')
}

func hexDigitValue(character byte) byte {
	switch {
	case character >= '0' && character <= '9':
		return character - '0'
	case character >= 'A' && character <= 'F':
		return character - 'A' + 10
	default:
		return character - 'a' + 10
	}
}
