package archive

import (
	"strings"

	"golang.org/x/net/html"
)

func htmlTextParts(markupParts []string) []string {
	textParts := make([]string, 0, len(markupParts))
	for _, markup := range markupParts {
		textParts = append(textParts, htmlToText(markup))
	}
	return textParts
}

// Deterministic carve-out: this only walks markup structure and emits text.
// It does not infer meaning, rank content, or choose between alternatives.
func htmlToText(markup string) string {
	root, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		return ""
	}
	var out strings.Builder
	writeHTMLText(&out, root)
	return collapseHTMLBlankLines(out.String())
}

func writeHTMLText(out *strings.Builder, node *html.Node) {
	tag := ""
	if node.Type == html.TextNode {
		out.WriteString(node.Data)
	}
	if node.Type == html.ElementNode {
		tag = strings.ToLower(node.Data)
		if skipHTMLTextElement(tag) {
			return
		}
		if htmlBlockElement(tag) {
			out.WriteByte('\n')
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		writeHTMLText(out, child)
	}
	if node.Type == html.ElementNode && tag != "br" && htmlBlockElement(tag) {
		out.WriteByte('\n')
	}
}

func skipHTMLTextElement(tag string) bool {
	switch tag {
	case "head", "script", "style":
		return true
	default:
		return false
	}
}

func htmlBlockElement(tag string) bool {
	switch tag {
	case "p", "div", "br", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "table":
		return true
	default:
		return false
	}
}

func collapseHTMLBlankLines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	out := make([]string, 0, len(lines))
	previousBlank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !previousBlank && len(out) > 0 {
				out = append(out, "")
				previousBlank = true
			}
			continue
		}
		out = append(out, line)
		previousBlank = false
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
