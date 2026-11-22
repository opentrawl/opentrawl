package mailparse

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// invisibleEmailPaddingCharacters are the invisible characters that bulk
// senders repeat after a message preview to hide the body from inbox
// previews. They carry no text. Joiners that some scripts need are kept.
var invisibleEmailPaddingCharacters = strings.NewReplacer(
	"\u034f", "", // combining grapheme joiner
	"\u200b", "", // zero width space
	"\u2060", "", // word joiner
	"\ufeff", "", // zero width no-break space
	"\u00ad", "", // soft hyphen
)

// NormalizeEmailBodyTextLineStructure keeps the sender's lines and
// paragraphs. It unifies line endings, removes trailing space on each line,
// removes blank lines at the start and end and joins runs of blank lines
// into one blank line.
func NormalizeEmailBodyTextLineStructure(bodyText string) string {
	bodyText = invisibleEmailPaddingCharacters.Replace(bodyText)
	bodyText = stripEmailPreheaderJoiners(bodyText)
	bodyText = strings.ReplaceAll(bodyText, "\r\n", "\n")
	bodyText = strings.ReplaceAll(bodyText, "\r", "\n")
	lines := strings.Split(bodyText, "\n")
	kept := make([]string, 0, len(lines))
	previousLineIsBlank := true
	for _, line := range lines {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		lineIsBlank := line == ""
		if lineIsBlank && previousLineIsBlank {
			continue
		}
		kept = append(kept, line)
		previousLineIsBlank = lineIsBlank
	}
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	return strings.Join(kept, "\n")
}

// stripEmailPreheaderJoiners removes U+200C zero-width non-joiner characters
// used as inbox-preview padding while preserving U+200C between letters in
// scripts that require it, and collapses any space run directly caused by
// removed padding.
func stripEmailPreheaderJoiners(text string) string {
	if !strings.ContainsRune(text, '\u200c') {
		return text
	}
	runes := []rune(text)
	var out strings.Builder
	out.Grow(len(text))
	removedPaddingInCurrentSpaceRun := false
	pendingSpace := false
	for index, r := range runes {
		if r == '\u200c' {
			hasLetterBefore := index > 0 && unicode.IsLetter(runes[index-1])
			hasLetterAfter := index+1 < len(runes) && unicode.IsLetter(runes[index+1])
			if hasLetterBefore && hasLetterAfter {
				if pendingSpace {
					out.WriteByte(' ')
					pendingSpace = false
				}
				removedPaddingInCurrentSpaceRun = false
				out.WriteRune(r)
			} else {
				removedPaddingInCurrentSpaceRun = true
			}
			continue
		}
		if r == '\u00a0' || r == '\u2007' || r == '\u202f' || r == ' ' || r == '\t' {
			if removedPaddingInCurrentSpaceRun {
				pendingSpace = true
				continue
			}
			out.WriteRune(r)
			continue
		}
		if r == '\n' || r == '\r' {
			pendingSpace = false
			removedPaddingInCurrentSpaceRun = false
			out.WriteRune(r)
			continue
		}
		if pendingSpace {
			out.WriteByte(' ')
			pendingSpace = false
		}
		removedPaddingInCurrentSpaceRun = false
		out.WriteRune(r)
	}
	return out.String()
}

// plainTextPartContainsHTMLDocument reports whether a part labelled text/plain
// actually starts with an HTML document and closes HTML structure, as some
// automated senders emit.
func plainTextPartContainsHTMLDocument(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	hasHTMLDocumentPrefix := strings.HasPrefix(trimmed, "<!doctype html") ||
		strings.HasPrefix(trimmed, "<html") ||
		strings.HasPrefix(trimmed, "<head") ||
		strings.HasPrefix(trimmed, "<body") ||
		strings.HasPrefix(trimmed, "<div") ||
		strings.HasPrefix(trimmed, "<table")
	if !hasHTMLDocumentPrefix {
		return false
	}
	return strings.Contains(trimmed, "</html>") ||
		strings.Contains(trimmed, "</body>") ||
		strings.Contains(trimmed, "</div>") ||
		strings.Contains(trimmed, "</table>")
}

// FlattenEmailTextToOneLine is only for one-line search snippets; stored and
// opened bodies keep their lines.
func FlattenEmailTextToOneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// htmlEmailPartText walks markup structure and writes its text: block
// elements start new lines, and source whitespace collapses as a browser
// collapses it. It does not infer meaning or choose between alternatives.
func htmlEmailPartText(markup string) string {
	root, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return ""
	}
	writer := htmlEmailTextWriter{}
	writer.writeNode(root, false)
	lines := strings.Split(writer.text.String(), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSpace(line)
	}
	return NormalizeEmailBodyTextLineStructure(strings.Join(lines, "\n"))
}

type htmlEmailTextWriter struct {
	text strings.Builder
}

func (writer *htmlEmailTextWriter) writeNode(node *html.Node, insidePreformattedText bool) {
	switch node.Type {
	case html.TextNode:
		if insidePreformattedText {
			writer.text.WriteString(node.Data)
		} else {
			writer.writeCollapsedWhitespaceText(node.Data)
		}
		return
	case html.CommentNode, html.DoctypeNode:
		return
	}
	tag := ""
	if node.Type == html.ElementNode {
		tag = strings.ToLower(node.Data)
		switch tag {
		case "head", "script", "style", "title", "template":
			return
		case "br":
			writer.text.WriteByte('\n')
			return
		case "pre":
			insidePreformattedText = true
		case "img":
			for _, attribute := range node.Attr {
				if strings.EqualFold(attribute.Key, "alt") {
					if altText := strings.TrimSpace(attribute.Val); altText != "" {
						writer.writeCollapsedWhitespaceText(altText)
					}
					break
				}
			}
		}
		if htmlBlockElementStartsNewLine(tag) {
			writer.text.WriteByte('\n')
		}
	}
	textLengthBeforeChildren := writer.text.Len()
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		writer.writeNode(child, insidePreformattedText)
	}
	if tag == "a" {
		writer.writeLinkTargetAfterLinkText(node, writer.text.String()[textLengthBeforeChildren:])
	}
	switch {
	case htmlBlockElementStartsNewLine(tag):
		writer.text.WriteByte('\n')
	case tag == "td" || tag == "th":
		writer.text.WriteByte(' ')
	}
}

// writeLinkTargetAfterLinkText keeps a web link's target, as plain-text email
// writes it: "Sign in (https://...)". A link without text, such as an icon,
// or whose text already is its target, adds nothing.
func (writer *htmlEmailTextWriter) writeLinkTargetAfterLinkText(anchor *html.Node, linkText string) {
	linkText = strings.TrimSpace(linkText)
	if linkText == "" {
		return
	}
	for _, attribute := range anchor.Attr {
		if !strings.EqualFold(attribute.Key, "href") {
			continue
		}
		target := strings.TrimSpace(attribute.Val)
		lowerTarget := strings.ToLower(target)
		if (strings.HasPrefix(lowerTarget, "https://") || strings.HasPrefix(lowerTarget, "http://")) && target != linkText {
			writer.text.WriteString(" (" + target + ")")
		}
		return
	}
}

func (writer *htmlEmailTextWriter) writeCollapsedWhitespaceText(text string) {
	previousIsSpace := false
	for _, character := range text {
		if unicode.IsSpace(character) {
			if !previousIsSpace {
				writer.text.WriteByte(' ')
			}
			previousIsSpace = true
			continue
		}
		writer.text.WriteRune(character)
		previousIsSpace = false
	}
}

func htmlBlockElementStartsNewLine(tag string) bool {
	switch tag {
	case "p", "div", "tr", "li", "ul", "ol", "dl", "dt", "dd", "table", "blockquote", "pre", "hr",
		"h1", "h2", "h3", "h4", "h5", "h6", "section", "article", "header", "footer", "center", "address":
		return true
	default:
		return false
	}
}
