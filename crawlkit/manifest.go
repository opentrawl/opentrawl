package crawlkit

import (
	"flag"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/openclaw/crawlkit/control"
)

func generateManifest(source Crawler, stateRoot, binaryName string) (control.Manifest, error) {
	info := source.Info()
	paths, err := resolveSourcePaths(stateRoot, info.ID)
	if err != nil {
		return control.Manifest{}, err
	}
	spine, err := spineVerbDeclarations(source)
	if err != nil {
		return control.Manifest{}, err
	}
	if strings.TrimSpace(binaryName) == "" {
		binaryName = filepathBase(os.Args[0])
	}
	display := firstText(info.DisplayName, info.Surface, info.ID)
	manifest := control.NewManifest(info.ID, display, binaryName)
	manifest.SchemaVersion = control.RunnerManifestVersion
	manifest.Version = buildVersion
	manifest.Description = strings.TrimSpace(info.Description)
	manifest.Privacy = info.Privacy
	manifest.Paths = control.Paths{
		DefaultConfig:   paths.Config,
		DefaultDatabase: paths.Archive,
		DefaultLogs:     paths.Logs,
	}
	manifest.Capabilities = capabilitiesFor(source, info)
	commandStateRoot := ""
	if strings.TrimSpace(stateRoot) != "" {
		commandStateRoot = paths.StateRoot
	}
	manifest.Commands = commandTable(source, binaryName, commandStateRoot, spine)
	return manifest, nil
}

func capabilitiesFor(source Crawler, info Info) []string {
	caps := []string{"metadata", "status", "doctor"}
	if _, ok := source.(Syncer); ok {
		caps = append(caps, "sync")
	}
	if _, ok := source.(Searcher); ok {
		caps = append(caps, "search")
	}
	if _, ok := source.(Opener); ok {
		caps = append(caps, "open")
	}
	if _, ok := source.(WhoMatcher); ok {
		caps = append(caps, "who")
	}
	if _, ok := source.(ContactExporter); ok {
		caps = append(caps, "contacts_export")
	}
	if info.ShortRefs {
		caps = append(caps, "short_refs")
	}
	for _, verb := range source.Verbs() {
		if _, ok := spineVerbKey(verb.Name); ok {
			continue
		}
		name := commandKey(verb.Name)
		if name != "" {
			caps = append(caps, name)
		}
	}
	return uniqueStrings(caps)
}

func commandTable(source Crawler, binaryName, stateRoot string, spine map[string]Verb) map[string]control.Command {
	commands := map[string]control.Command{
		"metadata": applySpineDeclaration(control.Command{Title: "Metadata", Argv: commandArgv(binaryName, stateRoot, "metadata"), JSON: true}, spine, "metadata"),
		"status":   applySpineDeclaration(control.Command{Title: "Status", Argv: commandArgv(binaryName, stateRoot, "status"), JSON: true}, spine, "status"),
		"doctor":   applySpineDeclaration(control.Command{Title: "Doctor", Argv: commandArgv(binaryName, stateRoot, "doctor"), JSON: true}, spine, "doctor"),
	}
	if _, ok := source.(Syncer); ok {
		commands["sync"] = applySpineDeclaration(control.Command{Title: "Sync", Argv: commandArgv(binaryName, stateRoot, "sync"), JSON: true, Mutates: true}, spine, "sync")
	}
	if _, ok := source.(Searcher); ok {
		_, supportsWho := source.(WhoMatcher)
		commands["search"] = applySpineDeclaration(control.Command{
			Title: "Search",
			Argv:  commandArgv(binaryName, stateRoot, "search", "QUERY"),
			JSON:  true,
			Flags: builtinSearchFlags(supportsWho),
		}, spine, "search")
	}
	if _, ok := source.(WhoMatcher); ok {
		commands["who"] = applySpineDeclaration(control.Command{Title: "Resolve person", Argv: commandArgv(binaryName, stateRoot, "who", "NAME"), JSON: true}, spine, "who")
	}
	if _, ok := source.(Opener); ok {
		commands["open"] = applySpineDeclaration(control.Command{Title: "Open", Argv: commandArgv(binaryName, stateRoot, "open", "REF"), JSON: true}, spine, "open")
	}
	if _, ok := source.(ContactExporter); ok {
		commands["contacts_export"] = applySpineDeclaration(control.Command{Title: "Export contacts", Argv: commandArgv(binaryName, stateRoot, "contacts", "export"), JSON: true}, spine, "contacts_export")
	}
	for _, verb := range source.Verbs() {
		if _, ok := spineVerbKey(verb.Name); ok {
			continue
		}
		key := commandKey(verb.Name)
		if key == "" {
			continue
		}
		argv := append([]string{binaryName}, strings.Fields(verb.Name)...)
		argv = append(argv, verb.Args...)
		argv = append(argv, "--json")
		argv = appendStateRoot(argv, stateRoot)
		commands[key] = control.Command{
			Title:   strings.TrimSpace(verb.Help),
			Argv:    argv,
			JSON:    true,
			Mutates: verb.Mutates,
			Flags:   flagsForVerb(verb),
		}
	}
	return commands
}

func applySpineDeclaration(command control.Command, spine map[string]Verb, key string) control.Command {
	verb, ok := spine[key]
	if !ok {
		return command
	}
	command.Flags = append(command.Flags, flagsForVerb(verb)...)
	sort.Slice(command.Flags, func(i, j int) bool { return command.Flags[i].Name < command.Flags[j].Name })
	return command
}

func commandArgv(binaryName, stateRoot string, args ...string) []string {
	argv := append([]string{binaryName}, args...)
	argv = append(argv, "--json")
	return appendStateRoot(argv, stateRoot)
}

func appendStateRoot(argv []string, stateRoot string) []string {
	stateRoot = strings.TrimSpace(stateRoot)
	if stateRoot == "" {
		return argv
	}
	return append(argv, "--state-root", stateRoot)
}

func flagsForVerb(verb Verb) []control.Flag {
	if verb.Flags == nil {
		return nil
	}
	fs := flag.NewFlagSet(verb.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	verb.Flags(fs)
	return flagsFromSet(fs)
}

func builtinSearchFlags(includeWho bool) []control.Flag {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	defineSearchFlags(fs, includeWho)
	return flagsFromSet(fs)
}

func flagsFromSet(fs *flag.FlagSet) []control.Flag {
	var flags []control.Flag
	fs.VisitAll(func(f *flag.Flag) {
		flags = append(flags, control.Flag{
			Name:    f.Name,
			Usage:   f.Usage,
			Default: f.DefValue,
		})
	})
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
}

func commandKey(name string) string {
	name = strings.Join(strings.Fields(strings.TrimSpace(name)), "_")
	name = strings.ReplaceAll(name, "-", "_")
	return name
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func filepathBase(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), string(os.PathSeparator))
	if path == "" {
		return "trawl"
	}
	if i := strings.LastIndexByte(path, os.PathSeparator); i >= 0 {
		return path[i+1:]
	}
	return path
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
