package trawlkit

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/usage"
)

func writeHelp(w io.Writer, source Crawler, target []string, stateRoot string) error {
	manifest, err := generateManifest(source, stateRoot, firstText(source.Info().Surface, source.Info().ID))
	if err != nil {
		return err
	}
	if len(target) == 0 {
		_, err = io.WriteString(w, topHelpDoc(manifest).Render())
		return err
	}
	key := helpCommandKey(target)
	command, ok := manifest.Commands[key]
	if !ok {
		return usageError{err: fmt.Errorf("unknown help topic %q", strings.Join(target, " "))}
	}
	_, err = io.WriteString(w, commandHelpDoc(manifest, key, command).Render())
	return err
}

func writeRootHelp(w io.Writer, sources []Crawler) error {
	commands := make([]usage.Command, 0, len(sources))
	for _, source := range sources {
		info := source.Info()
		commands = append(commands, usage.Command{
			Name:    firstText(info.Surface, info.ID),
			Summary: info.DisplayName,
		})
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	doc := usage.Doc{
		Tool: "trawl",
		Groups: []usage.Group{{
			Title:    "Sources",
			Commands: commands,
		}},
		Flags:  globalHelpFlags(),
		Footer: []string{"Run trawl SOURCE --help for source commands."},
	}
	_, err := io.WriteString(w, doc.Render())
	return err
}

func helpRequested(globals globalOptions) (bool, []string) {
	if len(globals.args) > 0 && globals.args[0] == "help" {
		return true, append([]string(nil), globals.args[1:]...)
	}
	if globals.help {
		return true, append([]string(nil), globals.args...)
	}
	return false, nil
}

func topHelpDoc(manifest control.Manifest) usage.Doc {
	return usage.Doc{
		Tool: firstText(manifest.Binary.Name, manifest.ID),
		Groups: []usage.Group{{
			Title:    "Commands",
			Commands: helpCommands(manifest),
		}},
		Flags:  globalHelpFlags(),
		Footer: helpFooter(manifest),
	}
}

func commandHelpDoc(manifest control.Manifest, key string, command control.Command) usage.Doc {
	return usage.Doc{
		Tool:    firstText(manifest.Binary.Name, manifest.ID) + " " + commandUsageName(command),
		Tagline: firstText(command.Title, key),
		Flags:   append(globalHelpFlags(), commandHelpFlags(command.Flags)...),
		Footer:  helpFooter(manifest),
	}
}

func helpCommands(manifest control.Manifest) []usage.Command {
	keys := make([]string, 0, len(manifest.Commands))
	for key := range manifest.Commands {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	commands := make([]usage.Command, 0, len(keys))
	for _, key := range keys {
		command := manifest.Commands[key]
		commands = append(commands, usage.Command{
			Name:    commandUsageName(command),
			Summary: command.Title,
		})
	}
	return commands
}

func commandHelpFlags(flags []control.Flag) []usage.Flag {
	out := make([]usage.Flag, 0, len(flags))
	for _, flag := range flags {
		name := commandHelpFlagName(flag)
		if name == "" {
			continue
		}
		out = append(out, usage.Flag{Name: name, Summary: commandHelpFlagSummary(flag)})
	}
	return out
}

func commandHelpFlagName(flag control.Flag) string {
	name := strings.TrimSpace(flag.Name)
	if name == "" {
		return ""
	}
	out := "--" + name
	if !commandHelpFlagIsBool(flag) {
		out += " VALUE"
	}
	return out
}

func commandHelpFlagIsBool(flag control.Flag) bool {
	switch strings.TrimSpace(flag.Default) {
	case "true", "false":
		return true
	default:
		return false
	}
}

func commandHelpFlagSummary(flag control.Flag) string {
	summary := strings.TrimSpace(flag.Usage)
	defaultValue := strings.TrimSpace(flag.Default)
	if commandHelpFlagIsBool(flag) || defaultValue == "" {
		return summary
	}
	if summary == "" {
		return "default " + defaultValue
	}
	return summary + " (default " + defaultValue + ")"
}

func globalHelpFlags() []usage.Flag {
	return []usage.Flag{
		{Name: "--json", Summary: "write JSON to stdout"},
		{Name: "-v, --verbose", Summary: "stream log lines to stderr"},
		{Name: "-vv", Summary: "stream debug log lines to stderr"},
		{Name: "--version", Summary: "print version and exit"},
		{Name: "-h, --help", Summary: "show help"},
	}
}

func helpFooter(manifest control.Manifest) []string {
	logPath := manifest.Paths.DefaultLogs
	if strings.TrimSpace(logPath) == "" {
		logPath = "~/.opentrawl/" + manifest.ID + "/logs/current.log"
	}
	return []string{"Diagnostics: run with -v, or read " + logPath}
}

func commandUsageName(command control.Command) string {
	var parts []string
	for _, arg := range command.Argv[1:] {
		if arg == "--json" {
			break
		}
		parts = append(parts, arg)
	}
	if len(parts) == 0 {
		return "metadata"
	}
	return strings.Join(parts, " ")
}

func helpCommandKey(target []string) string { return commandKey(strings.Join(target, " ")) }
