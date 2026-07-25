package cli

import (
	"fmt"
	"io"
	"strings"

	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func renderPresentation(w io.Writer, document *presentationv1.PresentationDocument) error {
	if document == nil {
		return fmt.Errorf("presentation document is nil")
	}
	wrote := false
	writeSection := func(write func() error) error {
		if wrote {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if err := write(); err != nil {
			return err
		}
		wrote = true
		return nil
	}
	title := strings.TrimSpace(document.GetTitle())
	if title != "" {
		if err := writeSection(func() error { _, err := fmt.Fprintln(w, title); return err }); err != nil {
			return err
		}
	}
	for index, block := range document.GetBlocks() {
		if index == 0 && title != "" && block != nil && strings.TrimSpace(block.GetHeading().GetText()) == title {
			continue
		}
		if err := writeSection(func() error { return writePresentationBlock(w, block) }); err != nil {
			return err
		}
	}
	if len(document.GetActions()) > 0 {
		section, err := renderPresentationActions(document.GetActions())
		if err != nil {
			return err
		}
		if err := writeSection(func() error { _, err := fmt.Fprintln(w, section); return err }); err != nil {
			return err
		}
	}
	if len(document.GetFacts()) > 0 {
		section, err := renderPresentationFacts(document.GetFacts())
		if err != nil {
			return err
		}
		if err := writeSection(func() error { _, err := fmt.Fprintln(w, section); return err }); err != nil {
			return err
		}
	}
	return nil
}

func writePresentationBlock(w io.Writer, block *presentationv1.Block) error {
	if block == nil {
		return fmt.Errorf("presentation block is nil")
	}
	switch content := block.Content.(type) {
	case *presentationv1.Block_Heading:
		_, err := fmt.Fprintln(w, content.Heading.GetText())
		return err
	case *presentationv1.Block_Prose:
		_, err := fmt.Fprintln(w, renderPresentationProse(w, content.Prose.GetText()))
		return err
	case *presentationv1.Block_Fields:
		for _, field := range content.Fields.GetFields() {
			if field == nil {
				return fmt.Errorf("presentation field is nil")
			}
			if err := render.WriteWrappedField(w, field.GetLabel(), field.GetDisplay()); err != nil {
				return err
			}
		}
		return nil
	case *presentationv1.Block_Table:
		return writePresentationTable(w, content.Table)
	case *presentationv1.Block_Resource:
		return writePresentationResource(w, content.Resource)
	default:
		return fmt.Errorf("presentation block has unknown content")
	}
}

func writePresentationTable(w io.Writer, table *presentationv1.Table) error {
	if table == nil {
		return fmt.Errorf("presentation table is nil")
	}
	columns := make([]render.TableColumn, 0, len(table.GetColumns()))
	for _, column := range table.GetColumns() {
		columns = append(columns, render.TableColumn{Header: column, Wrap: true})
	}
	rows := make([][]string, 0, len(table.GetRows()))
	for _, row := range table.GetRows() {
		if row == nil || row.GetRole() == presentationv1.Row_ROLE_UNSPECIFIED {
			return fmt.Errorf("presentation table row has unspecified role")
		}
		cells := make([]string, 0, len(row.GetCells()))
		for _, cell := range row.GetCells() {
			if cell == nil {
				return fmt.Errorf("presentation table cell is nil")
			}
			cells = append(cells, cell.GetDisplay())
		}
		if row.GetRole() == presentationv1.Row_ROLE_TARGET && len(cells) > 0 {
			cells[0] = "→ " + cells[0]
		}
		rows = append(rows, cells)
	}
	return render.WriteTable(w, columns, rows)
}

func writePresentationResource(w io.Writer, resource *presentationv1.Resource) error {
	if resource == nil {
		return fmt.Errorf("presentation resource is nil")
	}
	kind := ""
	switch resource.GetKind() {
	case presentationv1.Resource_KIND_FILE:
		kind = "File"
	case presentationv1.Resource_KIND_IMAGE:
		kind = "Image"
	case presentationv1.Resource_KIND_VIDEO:
		kind = "Video"
	case presentationv1.Resource_KIND_AUDIO:
		kind = "Audio"
	default:
		return fmt.Errorf("presentation resource has unspecified kind")
	}
	if _, err := fmt.Fprintf(w, "%s: %s\n", kind, resource.GetLabel()); err != nil {
		return err
	}
	if err := render.WriteWrappedField(w, "Ref", resource.GetRef()); err != nil {
		return err
	}
	for _, field := range resource.GetMetadata() {
		if field == nil {
			return fmt.Errorf("presentation resource metadata is nil")
		}
		if err := render.WriteWrappedField(w, field.GetLabel(), field.GetDisplay()); err != nil {
			return err
		}
	}
	return nil
}

func renderPresentationProse(w io.Writer, prose string) string {
	paragraphs := strings.Split(strings.ReplaceAll(prose, "\r\n", "\n"), "\n\n")
	lines := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		lines = append(lines, strings.Join(render.Wrap(paragraph, render.OutputWidth(w)), "\n"))
	}
	return strings.Join(lines, "\n\n")
}

func renderPresentationActions(actions []*presentationv1.Action) (string, error) {
	lines := make([]string, 0, len(actions))
	for _, action := range actions {
		if action == nil {
			return "", fmt.Errorf("presentation action is nil")
		}
		switch target := action.Target.(type) {
		case *presentationv1.Action_OpenRef:
			lines = append(lines, fmt.Sprintf("%s: trawl open %s", action.GetLabel(), target.OpenRef))
		case *presentationv1.Action_Url:
			lines = append(lines, fmt.Sprintf("%s: %s", action.GetLabel(), target.Url))
		default:
			return "", fmt.Errorf("presentation action has no target")
		}
	}
	return strings.Join(lines, "\n"), nil
}

func renderPresentationFacts(facts []*presentationv1.Fact) (string, error) {
	lines := make([]string, 0, len(facts)*2)
	for _, fact := range facts {
		if fact == nil {
			return "", fmt.Errorf("presentation fact is nil")
		}
		kind := ""
		switch fact.GetKind() {
		case presentationv1.Fact_KIND_TRUNCATION:
			kind = "Truncated"
		case presentationv1.Fact_KIND_PROVENANCE:
			kind = "Provenance"
		case presentationv1.Fact_KIND_WARNING:
			kind = "Warning"
		case presentationv1.Fact_KIND_ERROR:
			kind = "Error"
		default:
			return "", fmt.Errorf("presentation fact has unspecified kind")
		}
		lines = append(lines, fmt.Sprintf("%s: %s", kind, fact.GetMessage()))
		if remedy := fact.GetRemedy(); remedy != "" {
			lines = append(lines, "  Remedy: "+remedy)
		}
	}
	return strings.Join(lines, "\n"), nil
}
