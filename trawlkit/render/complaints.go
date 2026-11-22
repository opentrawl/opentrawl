package render

import (
	"strconv"
	"strings"
	"time"
)

// AgentComplaintEntry is one recorded complaint prepared for human or PM review.
type AgentComplaintEntry struct {
	RecordedAtTime              time.Time
	HostMachineName             string
	TrawlVersion                string
	CommandLine                 string
	OutputWidthColumns          int
	OutputWidthSource           string
	StandardOutputIsTerminal    bool
	AgentThreadLinkOrIdentifier string
	TryingToFind                string
	ComplaintText               string
	WorkaroundOrOutcome         string
}

// WriteAgentComplaintSaved confirms where a standalone complaint was stored.
func (output TrawlCommandOutput) WriteAgentComplaintSaved(complaintFilePath string) error {
	return writeLines(
		output.standardOutput,
		Wrap("Saved complaint to "+strings.TrimSpace(complaintFilePath)+".", OutputWidth(output.standardOutput)),
	)
}

// WriteAgentComplaints renders local agent complaints newest first as readable
// labelled blocks so nothing is truncated during review.
func (output TrawlCommandOutput) WriteAgentComplaints(
	complaints []AgentComplaintEntry,
	totalComplaintCount int,
) error {
	writer := output.standardOutput
	if len(complaints) == 0 {
		return writeLine(writer, "No complaints recorded.")
	}
	heading := "Complaints: showing " + FormatInteger(int64(len(complaints))) +
		" of " + FormatInteger(int64(totalComplaintCount)) + "."
	if err := writeLines(writer, Wrap(heading, OutputWidth(writer))); err != nil {
		return err
	}
	for _, entry := range complaints {
		if err := writeLine(writer, ""); err != nil {
			return err
		}
		headerParts := make([]string, 0, 3)
		if !entry.RecordedAtTime.IsZero() {
			headerParts = append(headerParts, entry.RecordedAtTime.Format("2006-01-02 15:04 -07:00"))
		}
		if host := strings.TrimSpace(entry.HostMachineName); host != "" {
			headerParts = append(headerParts, host)
		}
		if version := strings.TrimSpace(entry.TrawlVersion); version != "" {
			headerParts = append(headerParts, "trawl "+version)
		}
		if len(headerParts) > 0 {
			if err := writeLines(writer, Wrap(strings.Join(headerParts, " · "), OutputWidth(writer))); err != nil {
				return err
			}
		}
		if thread := strings.TrimSpace(entry.AgentThreadLinkOrIdentifier); thread != "" {
			if err := writeWrappedField(writer, "Thread", thread); err != nil {
				return err
			}
		}
		if commandLine := strings.TrimSpace(entry.CommandLine); commandLine != "" {
			if err := writeWrappedField(writer, "Command", commandLine); err != nil {
				return err
			}
		}
		if entry.OutputWidthColumns > 0 || strings.TrimSpace(entry.OutputWidthSource) != "" {
			ttyText := "false"
			if entry.StandardOutputIsTerminal {
				ttyText = "true"
			}
			widthSummary := strconv.Itoa(entry.OutputWidthColumns) + " cols (" + strings.TrimSpace(entry.OutputWidthSource) + ", tty=" + ttyText + ")"
			if err := writeWrappedField(writer, "Output", widthSummary); err != nil {
				return err
			}
		}
		if trying := strings.TrimSpace(entry.TryingToFind); trying != "" {
			if err := writeWrappedField(writer, "Trying", trying); err != nil {
				return err
			}
		}
		if complaintText := strings.TrimSpace(entry.ComplaintText); complaintText != "" {
			if err := writeWrappedField(writer, "Complaint", complaintText); err != nil {
				return err
			}
		}
		if workaround := strings.TrimSpace(entry.WorkaroundOrOutcome); workaround != "" {
			if err := writeWrappedField(writer, "Workaround", workaround); err != nil {
				return err
			}
		}
	}
	return nil
}
