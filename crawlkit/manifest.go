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
	paths, err := resolveSourcePaths(stateRoot, info)
	if err != nil {
		return control.Manifest{}, err
	}
	spine, err := spineVerbDeclarations(source)
	if err != nil {
		return control.Manifest{}, err
	}
	if err := validateBespokeVerbs(source); err != nil {
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
	manifest.Commands = commandTable(source, binaryName, spine)
	return manifest, nil
}

// Manifest returns the typed control manifest for an in-process crawler.
func Manifest(source Crawler) (control.Manifest, error) {
	return generateManifest(source, "", filepathBase(os.Args[0]))
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

func commandTable(source Crawler, binaryName string, spine map[string]Verb) map[string]control.Command {
	commands := map[string]control.Command{
		"metadata": applySpineDeclaration(spineCommand("Show crawler metadata", binaryName, "metadata", "metadata"), spine, "metadata"),
		"status":   applySpineDeclaration(spineCommand("Show archive status", binaryName, "status", "status"), spine, "status"),
		"doctor":   applySpineDeclaration(spineCommand("Check archive setup", binaryName, "doctor", "doctor"), spine, "doctor"),
	}
	if _, ok := source.(Syncer); ok {
		command := spineCommand("Sync the archive", binaryName, "sync", "sync")
		command.Mutates = true
		commands["sync"] = applySpineDeclaration(command, spine, "sync")
	}
	if _, ok := source.(Searcher); ok {
		_, supportsWho := source.(WhoMatcher)
		command := spineCommand("Search archive items", binaryName, "search", "search", "QUERY")
		command.Flags = builtinSearchFlags(supportsWho)
		commands["search"] = applySpineDeclaration(command, spine, "search")
	}
	if _, ok := source.(WhoMatcher); ok {
		commands["who"] = applySpineDeclaration(spineCommand("Resolve person", binaryName, "who", "who", "NAME"), spine, "who")
	}
	if _, ok := source.(Opener); ok {
		commands["open"] = applySpineDeclaration(spineCommand("Open an item", binaryName, "open", "open", "REF"), spine, "open")
	}
	if _, ok := source.(ContactExporter); ok {
		commands["contacts_export"] = applySpineDeclaration(spineCommand("Export contacts", binaryName, "contacts_export", "contacts", "export"), spine, "contacts_export")
	}
	for _, verb := range source.Verbs() {
		if _, ok := spineVerbKey(verb.Name); ok {
			continue
		}
		key := commandKey(verb.Name)
		if key == "" {
			continue
		}
		mode, _ := storeModeForVerb(verb)
		argv := append([]string{binaryName}, strings.Fields(verb.Name)...)
		argv = append(argv, verb.Args...)
		argv = append(argv, "--json")
		commands[key] = control.Command{
			Title:   strings.TrimSpace(verb.Help),
			Argv:    argv,
			JSON:    true,
			Mutates: verb.Mutates,
			Store:   storeModeManifestValue(mode),
			Flags:   flagsForVerb(verb),
		}
	}
	return commands
}

func spineCommand(title, binaryName, key string, args ...string) control.Command {
	return control.Command{
		Title: title,
		Argv:  commandArgv(binaryName, args...),
		JSON:  true,
		Store: storeModeManifestValue(spineDefaultStoreMode(key)),
	}
}

func applySpineDeclaration(command control.Command, spine map[string]Verb, key string) control.Command {
	verb, ok := spine[key]
	if !ok {
		return command
	}
	command.Store = storeModeManifestValue(spineStoreMode(key, &verb))
	command.Flags = append(command.Flags, flagsForVerb(verb)...)
	sort.Slice(command.Flags, func(i, j int) bool { return command.Flags[i].Name < command.Flags[j].Name })
	return command
}

func commandArgv(binaryName string, args ...string) []string {
	argv := append([]string{binaryName}, args...)
	return append(argv, "--json")
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
