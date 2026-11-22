package mailparse

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// emailSpacerHTMLEntities decodes literal HTML invisible-padding and spacer
// entities that bulk senders sometimes leak into text/plain parts (for
// example repeated "&#8199;&#847; " preheader padding). Normal HTML entities
// such as &lt; or &amp; are left intact so literal plain-text prose is not
// altered.
var emailSpacerHTMLEntities = strings.NewReplacer(
	"&#847;", "\u034f",
	"&#x34f;", "\u034f",
	"&#X34F;", "\u034f",
	"&#x034f;", "\u034f",
	"&#X034F;", "\u034f",
	"&#8199;", "\u2007",
	"&#x2007;", "\u2007",
	"&#X2007;", "\u2007",
	"&#8203;", "\u200b",
	"&#x200b;", "\u200b",
	"&#X200B;", "\u200b",
	"&#8204;", "\u200c",
	"&#x200c;", "\u200c",
	"&#X200C;", "\u200c",
	"&zwnj;", "\u200c",
	"&ZWNJ;", "\u200c",
	"&#8288;", "\u2060",
	"&#x2060;", "\u2060",
	"&#X2060;", "\u2060",
	"&#65279;", "\ufeff",
	"&#xfeff;", "\ufeff",
	"&#XFEFF;", "\ufeff",
	"&#173;", "\u00ad",
	"&#xad;", "\u00ad",
	"&#XAD;", "\u00ad",
	"&#x00ad;", "\u00ad",
	"&#X00AD;", "\u00ad",
	"&shy;", "\u00ad",
	"&SHY;", "\u00ad",
	"&nbsp;", "\u00a0",
	"&NBSP;", "\u00a0",
	"&#160;", "\u00a0",
	"&#xa0;", "\u00a0",
	"&#XA0;", "\u00a0",
	"&#x00a0;", "\u00a0",
	"&#X00A0;", "\u00a0",
)

// NormalizeEmailBodyTextLineStructure keeps the sender's lines and
// paragraphs. It unifies line endings, removes trailing space on each line,
// removes blank lines at the start and end and joins runs of blank lines
// into one blank line.
func NormalizeEmailBodyTextLineStructure(bodyText string) string {
	if strings.ContainsRune(bodyText, '&') {
		bodyText = emailSpacerHTMLEntities.Replace(bodyText)
	}
	bodyText = stripInvisibleEmailPaddingAndCollapseRuns(bodyText)
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

func isAlwaysInvisibleEmailPaddingRune(r rune) bool {
	switch r {
	case '\u034f', // combining grapheme joiner
		'\u200b', // zero width space
		'\u2060', // word joiner
		'\ufeff', // zero width no-break space
		'\u00ad': // soft hyphen
		return true
	default:
		return false
	}
}

func isEmailPaddingSpaceRune(r rune) bool {
	return r == '\u00a0' || r == '\u2007' || r == '\u202f' || r == ' ' || r == '\t'
}

// stripInvisibleEmailPaddingAndCollapseRuns removes invisible inbox-preview
// padding characters (preserving U+200C between letters in scripts that
// require it) and collapses any surrounding space or figure-space run caused
// by removed padding to at most one ordinary space.
func stripInvisibleEmailPaddingAndCollapseRuns(text string) string {
	hasInvisiblePadding := false
	for _, r := range text {
		if isAlwaysInvisibleEmailPaddingRune(r) || r == '\u200c' {
			hasInvisiblePadding = true
			break
		}
	}
	if !hasInvisiblePadding {
		return text
	}
	runes := []rune(text)
	var out strings.Builder
	out.Grow(len(text))
	var pendingSpaces []rune
	removedPaddingInCurrentSpaceRun := false
	flushSpaces := func() {
		if len(pendingSpaces) == 0 {
			return
		}
		if removedPaddingInCurrentSpaceRun {
			if out.Len() > 0 {
				out.WriteByte(' ')
			}
		} else {
			for _, spaceRune := range pendingSpaces {
				out.WriteRune(spaceRune)
			}
		}
		pendingSpaces = pendingSpaces[:0]
	}
	for index, r := range runes {
		if isAlwaysInvisibleEmailPaddingRune(r) {
			removedPaddingInCurrentSpaceRun = true
			continue
		}
		if r == '\u200c' {
			hasLetterBefore := index > 0 && unicode.IsLetter(runes[index-1])
			hasLetterAfter := index+1 < len(runes) && unicode.IsLetter(runes[index+1])
			if hasLetterBefore && hasLetterAfter {
				flushSpaces()
				removedPaddingInCurrentSpaceRun = false
				out.WriteRune(r)
			} else {
				removedPaddingInCurrentSpaceRun = true
			}
			continue
		}
		if isEmailPaddingSpaceRune(r) {
			pendingSpaces = append(pendingSpaces, r)
			continue
		}
		if r == '\n' || r == '\r' {
			pendingSpaces = pendingSpaces[:0]
			removedPaddingInCurrentSpaceRun = false
			out.WriteRune(r)
			continue
		}
		flushSpaces()
		removedPaddingInCurrentSpaceRun = false
		out.WriteRune(r)
	}
	if !removedPaddingInCurrentSpaceRun {
		flushSpaces()
	}
	return out.String()
}

// plainTextPartContainsHTMLDocument reports whether a part labelled text/plain
// actually carries an HTML document or embedded HTML markup, as some automated
// senders and ticketing systems emit.
func plainTextPartContainsHTMLDocument(text string) bool {
	return embeddedHTMLStartByteOffset(text) >= 0
}

func extractPlainTextPartWithHTMLDocument(text string) string {
	htmlStartOffset := embeddedHTMLStartByteOffset(text)
	if htmlStartOffset <= 0 {
		return htmlEmailPartText(text)
	}
	leadingPlainText := NormalizeEmailBodyTextLineStructure(text[:htmlStartOffset])
	htmlText := htmlEmailPartText(text[htmlStartOffset:])
	switch {
	case leadingPlainText != "" && htmlText != "":
		return leadingPlainText + "\n\n" + htmlText
	case leadingPlainText != "":
		return leadingPlainText
	default:
		return htmlText
	}
}

func embeddedHTMLStartByteOffset(text string) int {
	lower := strings.ToLower(text)
	offset := skipLeadingWhitespaceAndHTMLComments(lower)
	if offset >= len(lower) {
		return -1
	}
	rest := lower[offset:]
	if hasHTMLDocumentOpeningTagPrefix(rest) && countHTMLClosingTags(rest) >= 1 {
		return 0
	}
	if hasStrongEmbeddedHTMLMarker(rest) && countHTMLClosingTags(rest) >= 2 {
		if tagOffset := firstEmbeddedHTMLTagOffset(rest); tagOffset >= 0 {
			return offset + tagOffset
		}
	}
	return -1
}

func skipLeadingWhitespaceAndHTMLComments(lower string) int {
	index := 0
	for index < len(lower) {
		for index < len(lower) && (lower[index] == ' ' || lower[index] == '\t' || lower[index] == '\r' || lower[index] == '\n') {
			index++
		}
		if strings.HasPrefix(lower[index:], "<!--") {
			end := strings.Index(lower[index+4:], "-->")
			if end < 0 {
				return index
			}
			index += 4 + end + 3
			continue
		}
		break
	}
	return index
}

func hasHTMLDocumentOpeningTagPrefix(lower string) bool {
	for _, prefix := range []string{
		"<!doctype html",
		"<html",
		"<head",
		"<body",
		"<div",
		"<table",
		"<p>",
		"<p ",
		"<span",
		"<style",
		"<meta",
		"<center",
		"<font",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func hasStrongEmbeddedHTMLMarker(lower string) bool {
	for _, marker := range []string{
		"<!doctype html",
		"<html",
		"<style",
		"<meta ",
		"<table",
		"<div ",
		"<div>",
		"<span ",
		"<p ",
		"<!--startfragment-->",
		"<br />",
		"<br/>",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func countHTMLClosingTags(lower string) int {
	count := 0
	for _, closingTag := range []string{
		"</html>",
		"</body>",
		"</div>",
		"</table>",
		"</p>",
		"</span>",
		"</style>",
		"</a>",
		"</td>",
		"</tr>",
		"</center>",
		"</font>",
	} {
		if strings.Contains(lower, closingTag) {
			count++
		}
	}
	return count
}

func firstEmbeddedHTMLTagOffset(lower string) int {
	earliest := -1
	for _, tag := range []string{
		"<!doctype html",
		"<html",
		"<head",
		"<body",
		"<style",
		"<meta ",
		"<table",
		"<div",
		"<span",
		"<p>",
		"<p ",
		"<center",
		"<font",
		"<!--startfragment-->",
	} {
		if index := strings.Index(lower, tag); index >= 0 && (earliest < 0 || index < earliest) {
			earliest = index
		}
	}
	return earliest
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
	default:
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
