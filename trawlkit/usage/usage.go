// Package usage renders shared help text for crawler CLIs.
package usage

import (
	"fmt"
	"strings"
)

type Command struct {
	Name    string // "bookmarks"
	Summary string // "Tweets you bookmarked."
}

type Group struct {
	Title    string // "Read your archive"
	Commands []Command
}

type Flag struct {
	Name    string // "--db PATH"
	Summary string // "Archive database path."
}

type Doc struct {
	Tool     string // "birdcrawl"
	Tagline  string // "your X archive: tweets, bookmarks, likes and replies"
	Groups   []Group
	Flags    []Flag   // global flags
	Examples []string // literal example invocations
	Footer   []string // trailing lines, e.g. help hint, diagnostics pointer
}

func (d Doc) Render() string {
	width := d.nameWidth()
	sections := []string{header(d.Tool, d.Tagline)}
	for _, group := range d.Groups {
		if section := renderGroup(group, width); section != "" {
			sections = append(sections, section)
		}
	}
	if section := renderFlags(d.Flags, width); section != "" {
		sections = append(sections, section)
	}
	if section := renderIndentedSection("Examples", d.Examples); section != "" {
		sections = append(sections, section)
	}
	if section := renderFooter(d.Footer); section != "" {
		sections = append(sections, section)
	}
	return strings.Join(sections, "\n\n") + "\n"
}

func (d Doc) nameWidth() int {
	longest := 0
	for _, group := range d.Groups {
		for _, command := range group.Commands {
			longest = max(longest, len(strings.TrimSpace(command.Name)))
		}
	}
	for _, flag := range d.Flags {
		longest = max(longest, len(strings.TrimSpace(flag.Name)))
	}
	if longest == 0 {
		return 0
	}
	return longest + 2
}

func header(tool, tagline string) string {
	title := strings.TrimSpace(tool)
	if text := strings.TrimSpace(tagline); text != "" {
		return title + ": " + text
	}
	return title + ":"
}

func renderGroup(group Group, width int) string {
	title := strings.TrimSpace(group.Title)
	if title == "" {
		return ""
	}
	lines := []string{title + ":"}
	for _, command := range group.Commands {
		if line := renderNamedLine(command.Name, command.Summary, width); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func renderFlags(flags []Flag, width int) string {
	lines := []string{"Global flags:"}
	for _, flag := range flags {
		if line := renderNamedLine(flag.Name, flag.Summary, width); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func renderNamedLine(name, summary string, width int) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "  " + name
	}
	return fmt.Sprintf("  %-*s%s", width, name, summary)
}

func renderIndentedSection(title string, values []string) string {
	lines := []string{title + ":"}
	for _, value := range values {
		if line := strings.TrimSpace(value); line != "" {
			lines = append(lines, "  "+line)
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func renderFooter(values []string) string {
	var lines []string
	for _, value := range values {
		if line := strings.TrimSpace(value); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
