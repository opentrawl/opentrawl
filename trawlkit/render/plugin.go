package render

import (
	"strings"
)

// ExternalPluginRowPresentation carries one discovered standalone plugin for
// `trawl plugin list`.
type ExternalPluginRowPresentation struct {
	PluginIdentifier            string
	DisplayName                 string
	OriginLabel                 string
	StatusLabel                 string
	ExecutableSHA256Short       string
	ApprovedCapabilitiesSummary string
	ErrorSentence               string
}

// ExternalPluginListPresentation carries the full list of discovered standalone
// plugins rendered by `trawl plugin list`.
type ExternalPluginListPresentation struct {
	Plugins []ExternalPluginRowPresentation
}

// ExternalPluginInstalledPresentation carries the result of staging a standalone
// plugin executable via `trawl plugin install`.
type ExternalPluginInstalledPresentation struct {
	PluginIdentifier               string
	DisplayName                    string
	StagedBinaryPath               string
	ExecutableSHA256               string
	ApprovedCapabilitiesSummary    string
	UnapprovedDeclaredCapabilities []string
}

// ExternalPluginCommandUsageFailed formats a command-line usage error for
// `trawl plugin` subcommands.
func ExternalPluginCommandUsageFailed(reason string) CommandFailure {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return commandIsWrong("The plugin command arguments are not valid.")
	}
	return commandIsWrong(ensureTrailingPeriod(trimmed))
}

// ExternalPluginOperationFailed formats an operational failure for `trawl plugin`.
func ExternalPluginOperationFailed(reason string) CommandFailure {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return archiveOrOperationFailed("The plugin operation failed.")
	}
	return archiveOrOperationFailed(ensureTrailingPeriod(trimmed))
}

// WriteExternalPluginList renders discovered standalone plugins as compact rows.
func (output TrawlCommandOutput) WriteExternalPluginList(list ExternalPluginListPresentation) error {
	writer := output.standardOutput
	if len(list.Plugins) == 0 {
		return writeLine(writer, "No external plugins are installed.")
	}
	rows := make([][]string, 0, len(list.Plugins))
	for _, plugin := range list.Plugins {
		name := strings.TrimSpace(plugin.PluginIdentifier)
		if displayName := strings.TrimSpace(plugin.DisplayName); displayName != "" && !strings.EqualFold(displayName, name) {
			name = name + " (" + displayName + ")"
		}
		detail := strings.TrimSpace(plugin.ApprovedCapabilitiesSummary)
		if errText := strings.TrimSpace(plugin.ErrorSentence); errText != "" {
			detail = errText
		}
		if detail == "" {
			detail = "archive only"
		}
		rows = append(rows, []string{
			name,
			strings.TrimSpace(plugin.OriginLabel),
			strings.TrimSpace(plugin.StatusLabel),
			strings.TrimSpace(plugin.ExecutableSHA256Short),
			detail,
		})
	}
	return writeTable(writer, []TableColumn{
		{Header: "plugin", MinimumWidth: 10, NeverTruncateCellValues: true},
		{Header: "origin", MinimumWidth: 7, NeverTruncateCellValues: true},
		{Header: "status", MinimumWidth: 6, NeverTruncateCellValues: true},
		{Header: "sha256", MinimumWidth: 12, NeverTruncateCellValues: true},
		{Header: "capabilities", Wrap: true},
	}, rows, 4)
}

// WriteExternalPluginInstalled renders the outcome of staging a plugin executable.
func (output TrawlCommandOutput) WriteExternalPluginInstalled(installed ExternalPluginInstalledPresentation) error {
	writer := output.standardOutput
	width := OutputWidth(writer)
	label := strings.TrimSpace(installed.PluginIdentifier)
	if displayName := strings.TrimSpace(installed.DisplayName); displayName != "" && !strings.EqualFold(displayName, label) {
		label = displayName + " (" + label + ")"
	}
	if err := writeLines(writer, Wrap("Installed plugin "+label+".", width)); err != nil {
		return err
	}
	rows := [][]string{
		{"binary", strings.TrimSpace(installed.StagedBinaryPath)},
		{"sha256", strings.TrimSpace(installed.ExecutableSHA256)},
		{"approved", fallbackText(installed.ApprovedCapabilitiesSummary, "archive only")},
	}
	if err := writeTable(writer, []TableColumn{
		{Header: "field", MinimumWidth: 8, NeverTruncateCellValues: true},
		{Header: "value", Wrap: true},
	}, rows, 1); err != nil {
		return err
	}
	if len(installed.UnapprovedDeclaredCapabilities) > 0 {
		notice := "Not approved (re-run install with flags to grant): " + strings.Join(installed.UnapprovedDeclaredCapabilities, ", ") + "."
		if err := writeLine(writer, ""); err != nil {
			return err
		}
		if err := writeLines(writer, Wrap(notice, width)); err != nil {
			return err
		}
	}
	return writeNextCommands(writer, []NextCommand{{
		nextCommandLabel:                     "Check status",
		trawlCommandArgumentsAfterInvocation: []string{"status", strings.TrimSpace(installed.PluginIdentifier)},
	}})
}

// WriteExternalPluginRemoved renders the outcome of removing a staged plugin.
func (output TrawlCommandOutput) WriteExternalPluginRemoved(pluginIdentifier string, removed bool) error {
	writer := output.standardOutput
	id := strings.TrimSpace(pluginIdentifier)
	if !removed {
		return writeLine(writer, "No staged plugin named "+id+" was installed.")
	}
	return writeLine(writer, "Removed staged plugin "+id+". Its local archive was kept.")
}

func fallbackText(value string, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func ensureTrailingPeriod(sentence string) string {
	if strings.HasSuffix(sentence, ".") || strings.HasSuffix(sentence, "!") || strings.HasSuffix(sentence, "?") {
		return sentence
	}
	return sentence + "."
}
