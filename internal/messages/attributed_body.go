package messages

import (
	"bytes"
	"unicode/utf16"
	"unicode/utf8"
)

func decodeAttributedBody(body []byte) string {
	if !bytes.HasPrefix(body, []byte("\x04\x0bstreamtyped")) {
		return ""
	}
	marker := []byte("\x84\x01+")
	idx := bytes.Index(body, marker)
	if idx < 0 {
		return ""
	}
	pos := idx + len(marker)
	if pos >= len(body) {
		return ""
	}
	textLen, pos := decodeStreamtypedLength(body, pos)
	if textLen <= 0 || pos >= len(body) {
		return ""
	}
	for pos < len(body) && (body[pos] == 0x00 || body[pos] == 0x92 || (body[pos] >= 0x80 && body[pos] <= 0xbf)) {
		pos++
	}
	end := min(pos+textLen, len(body))
	return cleanDecodedText(body[pos:end])
}

func decodeStreamtypedLength(body []byte, pos int) (int, int) {
	first := body[pos]
	if first&0x80 == 0 {
		return int(first), pos + 1
	}
	width := int(first & 0x7f)
	pos++
	if width <= 0 || pos+width > len(body) {
		return 0, pos
	}
	var n int
	for i := 0; i < width; i++ {
		n |= int(body[pos+i]) << (8 * i)
	}
	return n, pos + width
}

func cleanDecodedText(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xff && raw[1] == 0xfe {
		return decodeUTF16(raw[2:], true)
	}
	if len(raw) >= 2 && raw[0] == 0xfe && raw[1] == 0xff {
		return decodeUTF16(raw[2:], false)
	}
	text := string(raw)
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func decodeUTF16(raw []byte, littleEndian bool) string {
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		if littleEndian {
			units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
		} else {
			units = append(units, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
	}
	return string(utf16.Decode(units))
}
