package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openclaw/crawlkit/render"
)

type SummariesCmd struct {
	Name string `arg:"" optional:"" help:"Summary name"`
}

func (SummariesCmd) Help() string {
	return "Summaries are precomputed documents over the archives; rows carry refs you can open with trawl open."
}

type summaryDocument struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Path    string `json:"path"`

	absolutePath string
}

type summariesEnvelope struct {
	Summaries []summaryDocument `json:"summaries"`
}

type summaryContentEnvelope struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

var errNoSummaries = errors.New("no summaries")

func (c *SummariesCmd) Run(r *Runtime) error {
	root, err := defaultDerivedDir()
	if err != nil {
		return err
	}
	docs, err := discoverSummaries(root)
	if errors.Is(err, errNoSummaries) {
		return r.writeNoSummaries(root)
	}
	if err != nil {
		return r.writeError("summaries_failed",
			"Could not read summaries.",
			"check "+tildePath(root))
	}
	if strings.TrimSpace(c.Name) == "" {
		if r.root.JSON {
			return writeJSON(r.stdout, summariesEnvelope{Summaries: docs})
		}
		return renderSummaries(r.stdout, docs)
	}
	return c.readSummary(r, docs)
}

func (c *SummariesCmd) readSummary(r *Runtime, docs []summaryDocument) error {
	name := strings.TrimSpace(c.Name)
	matches := matchingSummaries(docs, name)
	switch len(matches) {
	case 0:
		return r.writeError("unknown_summary",
			fmt.Sprintf("Summary %q was not found.", name),
			"run trawl summaries")
	case 1:
		data, err := os.ReadFile(matches[0].absolutePath)
		if err != nil {
			return r.writeError("read_summary_failed",
				fmt.Sprintf("Could not read summary %q.", name),
				"check "+matches[0].Path)
		}
		content := string(data)
		if r.root.JSON {
			return writeJSON(r.stdout, summaryContentEnvelope{
				Name:    matches[0].Name,
				Path:    matches[0].Path,
				Content: content,
			})
		}
		_, err = fmt.Fprint(r.stdout, stripFrontmatter(content))
		return err
	default:
		return r.writeError("ambiguous_summary",
			fmt.Sprintf("Summary %q is ambiguous: %s.", name, strings.Join(summaryPaths(matches), ", ")),
			"run trawl summaries")
	}
}

func defaultDerivedDir() (string, error) {
	stateRoot, crawlerID, err := trawlLogParts()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateRoot, crawlerID, "derived"), nil
}

// INDEX.md is the catalog: pipelines list what readers should see,
// so working files next to the documents never leak into the CLI.
func discoverSummaries(root string) ([]summaryDocument, error) {
	data, err := os.ReadFile(filepath.Join(root, "INDEX.md"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNoSummaries
		}
		return nil, err
	}
	var docs []summaryDocument
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		path, summary, ok := strings.Cut(strings.TrimPrefix(line, "- "), " - ")
		if !ok {
			continue
		}
		path = filepath.ToSlash(strings.TrimSpace(path))
		if filepath.Ext(path) != ".md" {
			continue
		}
		docs = append(docs, summaryDocument{
			Name:         strings.TrimSuffix(filepath.Base(path), ".md"),
			Summary:      strings.TrimSpace(summary),
			Path:         path,
			absolutePath: filepath.Join(root, filepath.FromSlash(path)),
		})
	}
	if len(docs) == 0 {
		return nil, errNoSummaries
	}
	return docs, nil
}

func renderSummaries(w io.Writer, docs []summaryDocument) error {
	if _, err := fmt.Fprintln(w, "Read: trawl summaries NAME"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	rows := make([][]string, 0, len(docs))
	for _, doc := range docs {
		rows = append(rows, []string{doc.Name, doc.Summary})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "name"},
		{Header: "summary", Wrap: true},
	}, rows)
}

func matchingSummaries(docs []summaryDocument, name string) []summaryDocument {
	var matches []summaryDocument
	for _, doc := range docs {
		if doc.Name == name {
			matches = append(matches, doc)
		}
	}
	return matches
}

func summaryPaths(docs []summaryDocument) []string {
	paths := make([]string, 0, len(docs))
	for _, doc := range docs {
		paths = append(paths, doc.Path)
	}
	sort.Strings(paths)
	return paths
}

func stripFrontmatter(content string) string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.TrimLeft(strings.Join(lines[i+1:], ""), "\r\n")
		}
	}
	return content
}

func (r *Runtime) writeNoSummaries(root string) error {
	return r.writeError("no_summaries",
		fmt.Sprintf("No summaries exist yet in %s.", tildePath(root)),
		"generate derived summaries, then run trawl summaries")
}
