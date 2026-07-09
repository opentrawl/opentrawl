package projection

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// ParagraphStyle style_type values. There is no proto enum — these are the
// bare int32 constants the reference implementation uses.
const (
	styleDefault    = -1  // plain paragraph, no prefix
	styleTitle      = 0   // note title — surfaced separately, emitted here without a prefix
	styleHeading    = 1   // "## "
	styleSubheading = 2   // "### "
	styleDottedList = 100 // "- "
	styleDashedList = 101 // "- "
	styleNumbered   = 102 // "1. ", "2. ", …
	styleCheckbox   = 103 // "- [ ] " / "- [x] "
)

// Field numbers within the attribute-run message tree.
const (
	fieldRunLength      = 1  // AttributeRun.length (UTF-16 code units)
	fieldParagraphStyle = 2  // AttributeRun.paragraph_style
	fieldAttachmentInfo = 12 // AttributeRun.attachment_info

	fieldStyleType    = 1 // ParagraphStyle.style_type
	fieldIndentAmount = 4 // ParagraphStyle.indent_amount
	fieldChecklist    = 5 // ParagraphStyle.checklist

	fieldChecklistDone = 2 // Checklist.done

	fieldAttachIdentifier = 1 // AttachmentInfo.attachment_identifier
	fieldAttachTypeUTI    = 2 // AttachmentInfo.type_uti
)

type paragraphStyle struct {
	present       bool
	styleType     int32
	indent        int
	checklist     bool
	checklistDone bool
}

type attachmentInfo struct {
	identifier string
	typeUTI    string
}

type attributeRun struct {
	length     int
	style      paragraphStyle
	attachment *attachmentInfo
}

func parseAttributeRuns(raw [][]byte) ([]attributeRun, error) {
	out := make([]attributeRun, 0, len(raw))
	for _, b := range raw {
		run, err := parseAttributeRun(b)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

func parseAttributeRun(b []byte) (attributeRun, error) {
	m, err := parse(b)
	if err != nil {
		return attributeRun{}, fmt.Errorf("attribute run: %w", err)
	}
	length, _ := m.varint(fieldRunLength)
	run := attributeRun{length: int(length)}

	if styleMsg, ok, err := m.child(fieldParagraphStyle); err != nil {
		return attributeRun{}, fmt.Errorf("paragraph style: %w", err)
	} else if ok {
		run.style, err = parseParagraphStyle(styleMsg)
		if err != nil {
			return attributeRun{}, err
		}
	}

	if attachMsg, ok, err := m.child(fieldAttachmentInfo); err != nil {
		return attributeRun{}, fmt.Errorf("attachment info: %w", err)
	} else if ok {
		identifier, _ := attachMsg.str(fieldAttachIdentifier)
		typeUTI, _ := attachMsg.str(fieldAttachTypeUTI)
		run.attachment = &attachmentInfo{identifier: identifier, typeUTI: typeUTI}
	}
	return run, nil
}

func parseParagraphStyle(m message) (paragraphStyle, error) {
	style := paragraphStyle{
		present:   true,
		styleType: m.int32(fieldStyleType, styleDefault),
		indent:    int(m.int32(fieldIndentAmount, 0)),
	}
	if checklist, ok, err := m.child(fieldChecklist); err != nil {
		return paragraphStyle{}, fmt.Errorf("checklist: %w", err)
	} else if ok {
		style.checklist = true
		done, _ := checklist.varint(fieldChecklistDone)
		style.checklistDone = done == 1
	}
	return style, nil
}

// renderNote walks the attribute runs, slicing note_text in UTF-16 code units
// (Apple's unit for AttributeRun.length), and emits one markdown line per
// paragraph. Attachment runs consume their length from the offset but emit an
// embed marker instead of a text slice.
func renderNote(note decodedNote, resolve TableResolver) string {
	units := utf16.Encode([]rune(note.text))
	offset := 0

	var lines []string
	var line strings.Builder
	var lineStyle paragraphStyle
	lineStyleSet := false
	active := paragraphStyle{styleType: styleDefault}
	number := 0

	setLineStyle := func(style paragraphStyle) {
		if !lineStyleSet {
			lineStyle = style
			lineStyleSet = true
		}
	}
	flush := func() {
		lines = append(lines, prefixFor(lineStyle, &number)+line.String())
		line.Reset()
		lineStyleSet = false
	}

	for _, run := range note.runs {
		if run.style.present {
			active = run.style
		}
		seg := take(units, &offset, run.length)

		if run.attachment != nil {
			setLineStyle(active)
			line.WriteString(markerFor(*run.attachment, resolve))
			continue
		}
		writeText(seg, active, setLineStyle, &line, flush)
	}
	// Emit any text the runs did not cover (a note with no runs, or runs whose
	// lengths undercount the text) as plain paragraphs rather than dropping it.
	if offset < len(units) {
		writeText(units[offset:], active, setLineStyle, &line, flush)
	}
	if line.Len() > 0 || lineStyleSet {
		flush()
	}
	return strings.Join(lines, "\n")
}

func writeText(seg []uint16, active paragraphStyle, setLineStyle func(paragraphStyle), line *strings.Builder, flush func()) {
	for _, r := range utf16.Decode(seg) {
		setLineStyle(active)
		if r == '\n' {
			flush()
			continue
		}
		line.WriteRune(r)
	}
}

// take consumes up to n UTF-16 units from units starting at *offset, advancing
// the offset. A run whose length overshoots the buffer (malformed) is clamped
// rather than panicking.
func take(units []uint16, offset *int, n int) []uint16 {
	start := *offset
	if start > len(units) {
		start = len(units)
	}
	end := start + n
	if end < start || end > len(units) {
		end = len(units)
	}
	*offset = end
	return units[start:end]
}

// prefixFor returns the markdown line prefix for a paragraph style and keeps
// the numbered-list counter. The counter increments across a contiguous run of
// numbered-list paragraphs and resets on any other style.
func prefixFor(style paragraphStyle, number *int) string {
	indent := strings.Repeat("  ", style.indent)
	switch {
	case style.styleType == styleCheckbox:
		*number = 0
		if style.checklistDone {
			return indent + "- [x] "
		}
		return indent + "- [ ] "
	case style.styleType == styleHeading:
		*number = 0
		return "## "
	case style.styleType == styleSubheading:
		*number = 0
		return "### "
	case style.styleType == styleDottedList || style.styleType == styleDashedList:
		*number = 0
		return indent + "- "
	case style.styleType == styleNumbered:
		*number++
		return fmt.Sprintf("%s%d. ", indent, *number)
	default:
		// Title (0), plain (-1), monospaced (4), block quotes, everything
		// else: no prefix. The title is surfaced separately by the read side.
		*number = 0
		return ""
	}
}
