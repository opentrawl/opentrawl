package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const agentComplaintsFileName = "complaints.jsonl"

type ComplainCmd struct {
	About      string   `name:"about" help:"Exact trawl command this complaint is about"`
	Trying     string   `name:"trying" help:"What you were trying to find or do"`
	Workaround string   `name:"workaround" help:"What got you past it, or that nothing did"`
	Thread     string   `name:"thread" help:"T3 thread link or session identifier"`
	Text       []string `arg:"" optional:"" name:"text" help:"What could not be found, was hard to find or was missing"`
}

type ComplaintsCmd struct {
	Limit int `name:"limit" default:"20" help:"Maximum complaints to show; 0 shows all"`
}

type storedAgentComplaintRecord struct {
	RecordedAtTimeRFC3339       string   `json:"recorded_at"`
	HostMachineName             string   `json:"host_machine_name"`
	TrawlVersion                string   `json:"trawl_version"`
	CommandArguments            []string `json:"command_arguments,omitempty"`
	AboutCommand                string   `json:"about_command,omitempty"`
	OutputWidthColumns          int      `json:"output_width_columns"`
	OutputWidthSource           string   `json:"output_width_source"`
	StandardOutputIsTerminal    bool     `json:"standard_output_is_terminal"`
	AgentThreadLinkOrIdentifier string   `json:"agent_thread_link_or_identifier,omitempty"`
	TryingToFind                string   `json:"trying_to_find,omitempty"`
	ComplaintText               string   `json:"complaint_text"`
	WorkaroundOrOutcome         string   `json:"workaround_or_outcome,omitempty"`
}

func (c *ComplainCmd) Run(r *Runtime) error {
	complaintText := strings.TrimSpace(strings.Join(c.Text, " "))
	if complaintText == "" {
		return render.ComplainNeedsText()
	}
	record := buildAgentComplaintRecord(
		r.now(),
		r.output.ResolvedOutputWidth(),
		nil,
		c.About,
		c.Trying,
		complaintText,
		c.Workaround,
		c.Thread,
	)
	complaintPath, err := appendAgentComplaintRecord(r.stateRoot, record)
	if err != nil {
		return err
	}
	return r.output.WriteAgentComplaintSaved(complaintPath)
}

func (c *ComplaintsCmd) Run(r *Runtime) error {
	if c.Limit < 0 {
		return render.LimitMustBeAtLeastOne()
	}
	records, err := readAgentComplaintRecords(r.stateRoot)
	if err != nil {
		return err
	}
	totalCount := len(records)
	// Reverse to newest-first order.
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
	if c.Limit > 0 && len(records) > c.Limit {
		records = records[:c.Limit]
	}
	entries := make([]render.AgentComplaintEntry, 0, len(records))
	for _, record := range records {
		recordedAt, _ := time.Parse(time.RFC3339, record.RecordedAtTimeRFC3339)
		commandLine := strings.TrimSpace(record.AboutCommand)
		if commandLine == "" && len(record.CommandArguments) > 0 {
			commandLine = formatCommandArgumentsForComplaintDisplay(record.CommandArguments)
		}
		entries = append(entries, render.AgentComplaintEntry{
			RecordedAtTime:              recordedAt,
			HostMachineName:             record.HostMachineName,
			TrawlVersion:                record.TrawlVersion,
			CommandLine:                 commandLine,
			OutputWidthColumns:          record.OutputWidthColumns,
			OutputWidthSource:           record.OutputWidthSource,
			StandardOutputIsTerminal:    record.StandardOutputIsTerminal,
			AgentThreadLinkOrIdentifier: record.AgentThreadLinkOrIdentifier,
			TryingToFind:                record.TryingToFind,
			ComplaintText:               record.ComplaintText,
			WorkaroundOrOutcome:         record.WorkaroundOrOutcome,
		})
	}
	return r.output.WriteAgentComplaints(entries, totalCount)
}

// extractGlobalComplaintFlag removes --complaint TEXT or --complaint=TEXT from
// args before command dispatch so a complaint is saved even if the command fails.
func extractGlobalComplaintFlag(args []string) (complaintText string, remainingArgs []string, found bool, err error) {
	remainingArgs = make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			remainingArgs = append(remainingArgs, args[index:]...)
			break
		}
		if arg == "--complaint" {
			if index+1 >= len(args) {
				return "", nil, false, render.OptionNeedsValue("--complaint")
			}
			value := strings.TrimSpace(args[index+1])
			if value == "" {
				return "", nil, false, render.OptionNeedsValue("--complaint")
			}
			complaintText = value
			found = true
			index++
			continue
		}
		if strings.HasPrefix(arg, "--complaint=") {
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--complaint="))
			if value == "" {
				return "", nil, false, render.OptionNeedsValue("--complaint")
			}
			complaintText = value
			found = true
			continue
		}
		remainingArgs = append(remainingArgs, arg)
	}
	return complaintText, remainingArgs, found, nil
}

func buildAgentComplaintRecord(
	recordedAt time.Time,
	widthDecision render.OutputWidthDecision,
	commandArguments []string,
	aboutCommand string,
	tryingToFind string,
	complaintText string,
	workaroundOrOutcome string,
	explicitThread string,
) storedAgentComplaintRecord {
	hostName, _ := os.Hostname()
	thread := strings.TrimSpace(explicitThread)
	if thread == "" {
		thread = detectAgentThreadIdentifierFromEnvironment()
	}
	var copiedArguments []string
	if len(commandArguments) > 0 {
		copiedArguments = append([]string(nil), commandArguments...)
	}
	return storedAgentComplaintRecord{
		RecordedAtTimeRFC3339:       recordedAt.Format(time.RFC3339),
		HostMachineName:             strings.TrimSpace(hostName),
		TrawlVersion:                Version,
		CommandArguments:            copiedArguments,
		AboutCommand:                strings.TrimSpace(aboutCommand),
		OutputWidthColumns:          widthDecision.WidthInColumns,
		OutputWidthSource:           string(widthDecision.WidthSource),
		StandardOutputIsTerminal:    widthDecision.StandardOutputIsTerminal,
		AgentThreadLinkOrIdentifier: thread,
		TryingToFind:                strings.TrimSpace(tryingToFind),
		ComplaintText:               strings.TrimSpace(complaintText),
		WorkaroundOrOutcome:         strings.TrimSpace(workaroundOrOutcome),
	}
}

func detectAgentThreadIdentifierFromEnvironment() string {
	for _, environmentVariableName := range []string{
		"T3CODE_THREAD_ID",
		"CODEX_THREAD_ID",
		"ANTIGRAVITY_CONVERSATION_ID",
		"CLAUDE_SESSION_ID",
	} {
		if value := strings.TrimSpace(os.Getenv(environmentVariableName)); value != "" {
			return value
		}
	}
	return ""
}

func agentComplaintsFilePath(stateRoot string) string {
	return filepath.Join(stateRoot, agentComplaintsFileName)
}

func appendAgentComplaintRecord(stateRoot string, record storedAgentComplaintRecord) (string, error) {
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	encoded = append(encoded, '\n')
	path := agentComplaintsFilePath(stateRoot)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(encoded)
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return path, nil
}

func readAgentComplaintRecords(stateRoot string) ([]storedAgentComplaintRecord, error) {
	path := agentComplaintsFilePath(stateRoot)
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var records []storedAgentComplaintRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record storedAgentComplaintRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func formatCommandArgumentsForComplaintDisplay(args []string) string {
	formatted := make([]string, 0, len(args)+1)
	formatted = append(formatted, "trawl")
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'") || arg == "" {
			formatted = append(formatted, strconv.Quote(arg))
			continue
		}
		formatted = append(formatted, arg)
	}
	return strings.Join(formatted, " ")
}
