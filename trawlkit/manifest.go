package trawlkit

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func generateManifest(source Crawler, stateRoot, binaryName string) (control.Manifest, error) {
	info := source.Info()
	if err := validateHeadlines(info.Headlines); err != nil {
		return control.Manifest{}, err
	}
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
	manifest.Aliases = trimmedAliases(info.Aliases)
	manifest.Privacy = info.Privacy
	manifest.Paths = control.Paths{
		DefaultConfig:   paths.Config,
		DefaultDatabase: paths.Archive,
		DefaultLogs:     paths.Logs,
	}
	manifest.Capabilities = capabilitiesFor(source, info)
	manifest.Commands = commandTable(source, binaryName, spine)
	manifest.Headlines = append([]string(nil), info.Headlines...)
	return manifest, nil
}

// Manifest returns the typed control manifest for an in-process crawler.
func Manifest(source Crawler) (control.Manifest, error) {
	return generateManifest(source, "", filepathBase(os.Args[0]))
}

func capabilitiesFor(source Crawler, info Info) []string {
	caps := []string{"metadata", "status"}
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
	if _, ok := source.(ChatLister); ok {
		caps = append(caps, "chats")
	}
	caps = append(caps, "short_refs")
	for _, verb := range source.Verbs() {
		if verb.Internal {
			continue
		}
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
	}
	if _, ok := source.(Syncer); ok {
		command := spineCommand("Sync the archive", binaryName, "sync", "sync")
		command.Mutates = true
		commands["sync"] = applySpineDeclaration(command, spine, "sync")
	}
	if command, ok := searchCommand(source, binaryName, spine); ok {
		commands["search"] = command
	}
	if _, ok := source.(WhoMatcher); ok {
		commands["who"] = applySpineDeclaration(spineCommand("Resolve person", binaryName, "who", "who", "NAME"), spine, "who")
	}
	if command, ok := chatsCommand(source, binaryName, spine); ok {
		commands["chats"] = command
	}
	if _, ok := source.(Opener); ok {
		commands["open"] = applySpineDeclaration(spineCommand("Open an item", binaryName, "open", "open", "REF"), spine, "open")
	}
	for _, verb := range source.Verbs() {
		if verb.Internal {
			continue
		}
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
			Title:     strings.TrimSpace(verb.Help),
			Argv:      argv,
			JSON:      true,
			Mutates:   verb.Mutates,
			Store:     storeModeManifestValue(mode),
			Secondary: verb.Secondary,
			Flags:     flagsForVerb(verb),
		}
	}
	return commands
}

func validateHeadlines(headlines []string) error {
	if len(headlines) > 4 {
		return fmt.Errorf("invalid headlines: at most four entries are allowed")
	}
	seen := make(map[string]struct{}, len(headlines))
	for _, headline := range headlines {
		if headline == "" {
			return fmt.Errorf("invalid headlines: entries must not be empty")
		}
		if strings.TrimSpace(headline) != headline {
			return fmt.Errorf("invalid headlines: entries must already be trimmed")
		}
		if _, ok := seen[headline]; ok {
			return fmt.Errorf("invalid headlines: entries must be distinct")
		}
		seen[headline] = struct{}{}
	}
	return nil
}

func chatsCommand(source Crawler, binaryName string, spine map[string]Verb) (control.Command, bool) {
	if _, ok := source.(ChatLister); !ok {
		return control.Command{}, false
	}
	command := spineCommand("List chats", binaryName, "chats", "chats")
	command.Flags = builtinChatFlags()
	return applySpineDeclaration(command, spine, "chats"), true
}

func searchCommand(source Crawler, binaryName string, spine map[string]Verb) (control.Command, bool) {
	if _, ok := source.(Searcher); !ok {
		return control.Command{}, false
	}
	_, supportsWho := source.(WhoMatcher)
	command := spineCommand("Search archive items", binaryName, "search", "search", "QUERY")
	command.Flags = builtinSearchFlags(supportsWho)
	return applySpineDeclaration(command, spine, "search"), true
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

func trimmedAliases(aliases []string) []string {
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if trimmed := strings.TrimSpace(alias); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
