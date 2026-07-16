package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

// This is the progressive-discovery seam. `trawl <source>` opens one
// crawler's own verbs as a namespace: the listing is served from its
// manifest, and `trawl <source> <verb>` runs that crawler through the
// same trawlkit registration trawl uses for top-level fan-out.
//
// The top-level commands (status, sync, search, who, chats, open) are a
// separate, permanent surface: they fan a single request out across every
// discovered source and render one typed, uniform result (a status table,
// a merged search, a who resolution). `trawl <source> <verb>` instead
// streams one crawler's own raw output untouched. Both read the same
// compiled trawlkit registrations; there is no second crawler list in
// trawl.

// namespaceCandidate reports the first non-flag token when it is not a
// built-in command — a token that can only be a source or a typo. The
// source lookup that tells the two apart is deferred to dispatch so the
// built-in fast path never pays for discovery.
func namespaceCandidate(args []string) (string, bool) {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if reservedCommand(arg) {
			return "", false
		}
		return arg, true
	}
	return "", false
}

func reservedCommand(name string) bool {
	switch name {
	case "status", "sync", "search", "summaries", "who", "chats", "open", "help":
		return true
	default:
		return false
	}
}

// namespaceRoot reads the global flags off the raw args, since the
// namespace path runs before kong parses them.
func namespaceRoot(args []string) *CLI {
	return &CLI{JSON: hasJSONFlag(args), Verbose: verboseLevel(args)}
}

func verboseLevel(args []string) int {
	level := 0
	for _, arg := range args {
		switch arg {
		case "-vv":
			level = 2
		case "-v", "--verbose":
			if level < 1 {
				level = 1
			}
		}
	}
	return level
}

func (r *Runtime) dispatchNamespace(args []string, token string) error {
	sources := discoverCrawlers(r.ctx)
	source, ok := findSource(sources, token)
	if !ok {
		return ckoutput.WriteJSONErrorIfNeeded(r.stdout, r.root.JSON, unknownCommandErr(token, sourceTokens(sources)))
	}
	if source.MetadataErr != nil {
		return r.writeError("crawler_unidentified",
			fmt.Sprintf("%s did not identify itself.", sourceHumanName(source)),
			fmt.Sprintf("run trawl status %s", sourceCommandToken(source)))
	}
	rest := argsAfter(args, token)
	if firstNonFlag(rest) == "" {
		return r.renderNamespace(source, token)
	}
	return r.runNamespaceVerb(source, token, rest)
}

func (r *Runtime) runNamespaceVerb(source Source, token string, rest []string) error {
	command, ok := namespaceMatch(source, rest)
	if !ok {
		leading := leadingLiterals(rest)
		if len(leading) == 0 {
			// The first token is a crawler flag: the verb came after its
			// flags. Name the shape, not the flag value.
			return r.writeError("unknown_verb",
				fmt.Sprintf("%s needs the verb first, before any flags.", sourceHumanName(source)),
				fmt.Sprintf("run trawl %s", token))
		}
		return r.writeError("unknown_verb",
			fmt.Sprintf("%s has no verb %q.", sourceHumanName(source), strings.Join(leading, " ")),
			fmt.Sprintf("run trawl %s", token))
	}
	runArgs := append([]string{source.ID}, rest...)
	if r.root.JSON && command.JSON && !containsArg(rest, "--json") {
		runArgs = append(runArgs, "--json")
	}
	verb := firstNonFlag(rest)
	started := r.logSourceStart(source, verb)
	out, captureErr := runTrawlkitCaptured(r.ctx, runArgs, []trawlkit.Crawler{source.Crawler})
	if len(out.Stdout) > 0 {
		_, _ = r.stdout.Write(out.Stdout)
	}
	if len(out.Stderr) > 0 {
		_, _ = r.lockedStderr().Write(out.Stderr)
	}
	err := captureErr
	if err == nil && out.Code != 0 {
		err = exitErr{code: out.Code}
	}
	r.logSourceDone(source, verb, started, err)
	return err
}

// sourceTokens lists each installed crawler by the same canonical name the
// front door shows — the surface with any declared alias in parentheses, e.g.
// "x (twitter)" — so the tool's "valid sources" list never disagrees with its
// own Sources block.
func sourceTokens(sources []Source) []string {
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, sourceBlockName(source))
	}
	sort.Strings(names)
	return names
}

// renderNamespace lists a crawler's verbs. Verbs come straight from the
// manifest so implementation plumbing is never named; the invocation
// column is exactly what the user types.
func (r *Runtime) renderNamespace(source Source, token string) error {
	verbs := namespaceVerbList(source)
	if r.root.JSON {
		return writeJSON(r.stdout, namespaceListing{
			Source:  source.ID,
			Surface: source.DisplayName,
			Verbs:   verbs,
		})
	}
	header := sourceHumanName(source)
	if _, err := fmt.Fprintf(r.stdout, "%s\n\n", header); err != nil {
		return err
	}
	if len(verbs) == 0 {
		_, err := fmt.Fprintln(r.stdout, "This crawler exposes no verbs.")
		return err
	}
	primary, secondary := splitSecondaryVerbs(verbs)
	width := verbColumnWidth(verbs)
	if err := writeVerbGroup(r.stdout, "Verbs:", primary, width); err != nil {
		return err
	}
	if len(secondary) > 0 {
		if _, err := fmt.Fprintln(r.stdout); err != nil {
			return err
		}
		if err := writeVerbGroup(r.stdout, "More verbs:", secondary, width); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(r.stdout, "\nRun a verb: trawl %s <verb>\n", token)
	return err
}

// splitSecondaryVerbs keeps the headline verbs first and the specialist ones in
// a separate group, preserving the alphabetical order within each.
func splitSecondaryVerbs(verbs []namespaceVerb) (primary, secondary []namespaceVerb) {
	for _, verb := range verbs {
		if verb.Secondary {
			secondary = append(secondary, verb)
			continue
		}
		primary = append(primary, verb)
	}
	return primary, secondary
}

func verbColumnWidth(verbs []namespaceVerb) int {
	width := 0
	for _, verb := range verbs {
		if len(verb.Verb) > width {
			width = len(verb.Verb)
		}
	}
	return width
}

func writeVerbGroup(w io.Writer, heading string, verbs []namespaceVerb, width int) error {
	if _, err := fmt.Fprintln(w, heading); err != nil {
		return err
	}
	for _, verb := range verbs {
		if _, err := fmt.Fprintf(w, "  %-*s  %s\n", width, verb.Verb, verb.Title); err != nil {
			return err
		}
	}
	return nil
}

type namespaceListing struct {
	Source  string          `json:"source"`
	Surface string          `json:"surface"`
	Verbs   []namespaceVerb `json:"verbs"`
}

type namespaceVerb struct {
	Verb      string `json:"verb"`
	Title     string `json:"title,omitempty"`
	Secondary bool   `json:"secondary,omitempty"`
}

func namespaceVerbList(source Source) []namespaceVerb {
	verbs := make([]namespaceVerb, 0, len(source.Commands))
	for _, command := range source.Commands {
		invocation := commandInvocation(command)
		if invocation == "" || rootOwnedNamespaceVerb(invocation) {
			continue
		}
		verbs = append(verbs, namespaceVerb{Verb: invocation, Title: command.Title, Secondary: command.Secondary})
	}
	sort.Slice(verbs, func(i, j int) bool { return verbs[i].Verb < verbs[j].Verb })
	return verbs
}

// namespaceMatch finds the manifest command whose literal prefix the
// request's leading tokens complete. It matches the full prefix, not just
// the first token, so an incomplete verb — "contacts" without its "export"
// — gets a trawl-owned error instead of reaching trawlkit.
func namespaceMatch(source Source, rest []string) (control.Command, bool) {
	leading := leadingLiterals(rest)
	if len(leading) == 0 {
		return control.Command{}, false
	}
	for _, command := range source.Commands {
		prefix := fixedVerbTokens(command)
		if len(prefix) > 0 && rootOwnedNamespaceVerb(strings.Join(prefix, " ")) {
			continue
		}
		if len(prefix) > 0 && tokensHavePrefix(leading, prefix) {
			return command, true
		}
	}
	return control.Command{}, false
}

// Sync is one root operation because it also owns cross-source lifecycle work
// such as updating People. Source namespaces expose source-native reads only.
func rootOwnedNamespaceVerb(invocation string) bool {
	verb := firstNonFlag(strings.Fields(invocation))
	return verb == "doctor" || verb == "sync"
}

// fixedVerbTokens is the literal command path the user must type: the
// manifest argv up to its first placeholder or the trailing --json, minus
// the source token. Manifest placeholders are UPPERCASE by convention (REF,
// QUERY, NAME) — an exact, stable structural check (rules §1.5), so
// everything from the first placeholder on is user-supplied.
func fixedVerbTokens(command control.Command) []string {
	if len(command.Argv) < 2 {
		return nil
	}
	var out []string
	for _, token := range command.Argv[1:] {
		if token == "--json" || isPlaceholder(token) {
			break
		}
		out = append(out, token)
	}
	return out
}

func isPlaceholder(token string) bool {
	hasLetter := false
	for _, r := range token {
		if unicode.IsLetter(r) {
			hasLetter = true
			if !unicode.IsUpper(r) {
				return false
			}
		}
	}
	return hasLetter
}

// leadingLiterals returns the verb words: the run of literal tokens after
// any trawl global flags (--json, -v) the caller placed ahead of the
// verb, stopping at the first crawler flag. So `trawl imessage --json chats`
// still finds "chats", while `chats --limit 5` stops the verb at "chats".
func leadingLiterals(rest []string) []string {
	var out []string
	for _, arg := range rest {
		if isGlobalFlag(arg) {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			break
		}
		out = append(out, arg)
	}
	return out
}

func tokensHavePrefix(tokens, prefix []string) bool {
	if len(tokens) < len(prefix) {
		return false
	}
	for i, want := range prefix {
		if tokens[i] != want {
			return false
		}
	}
	return true
}

// commandInvocation is what the user types for a manifest command: the
// argv minus the source token and the trailing --json the manifest carries for
// programmatic callers. Placeholder args (REF, QUERY) stay, so the
// listing shows that a verb takes an argument.
func commandInvocation(command control.Command) string {
	if len(command.Argv) < 2 {
		return ""
	}
	tokens := command.Argv[1:]
	if tokens[len(tokens)-1] == "--json" {
		tokens = tokens[:len(tokens)-1]
	}
	return strings.Join(tokens, " ")
}

func argsAfter(args []string, token string) []string {
	for i, arg := range args {
		if arg == token {
			return args[i+1:]
		}
	}
	return nil
}

func firstNonFlag(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
