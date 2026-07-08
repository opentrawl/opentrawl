package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/openclaw/clawdex/internal/apple"
	"github.com/openclaw/clawdex/internal/avatar"
	"github.com/openclaw/clawdex/internal/birdclaw"
	"github.com/openclaw/clawdex/internal/contactexport"
	"github.com/openclaw/clawdex/internal/discrawl"
	"github.com/openclaw/clawdex/internal/google"
	"github.com/openclaw/clawdex/internal/index"
	"github.com/openclaw/clawdex/internal/markdown"
	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/clawdex/internal/repo"
	"github.com/openclaw/clawdex/internal/vcard"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/flags"
	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
)

var Version = "dev"

type CLI struct {
	Config  string `name:"config" help:"Config path" env:"CLAWDEX_CONFIG"`
	Repo    string `name:"repo" help:"Contacts data repo path" env:"CLAWDEX_REPO"`
	JSON    bool   `name:"json" help:"Write JSON to stdout"`
	DryRun  bool   `name:"dry-run" short:"n" help:"Preview changes without writing"`
	NoInput bool   `name:"no-input" help:"Never prompt"`
	Verbose bool   `name:"verbose" short:"v" help:"Stream diagnostics to stderr. Use -vv for debug detail."`

	Version kong.VersionFlag `name:"version" help:"Print version and exit"`

	Metadata MetadataCmd `cmd:"" help:"Print control metadata"`
	Init     InitCmd     `cmd:"" help:"Initialise a contacts data repo"`
	Status   StatusCmd   `cmd:"" help:"Show contacts repo status"`
	ConfigC  ConfigCmd   `cmd:"" name:"config" help:"Show or edit contacts config"`
	Person   PersonCmd   `cmd:"" help:"Read people"`
	Contacts ContactsCmd `cmd:"" help:"Export contact JSON"`
	Who      WhoCmd      `cmd:"" help:"Resolve a person by name, alias, or identifier"`
	Search   SearchCmd   `cmd:"" help:"Search people and notes"`
	Import   ImportCmd   `cmd:"" help:"Import contacts into local markdown"`
	Sync     SyncCmd     `cmd:"" help:"Preview sync with address books"`
	Export   ExportCmd   `cmd:"" help:"Export vCard files"`
	Git      GitCmd      `cmd:"" help:"Run data repo git helpers"`
	Doctor   DoctorCmd   `cmd:"" help:"Check repo health"`
}

type Runtime struct {
	ctx        context.Context
	stdout     io.Writer
	stderr     io.Writer
	root       *CLI
	command    string
	verbosity  int
	configPath string
	cfg        repo.Config
	repo       repo.Repo
	store      index.Store
	runLog     *logRun
}

func Execute(args []string, stdout, stderr io.Writer) error {
	verbosity, args := pullVerbosity(args)
	var root CLI
	parser, err := kong.New(&root,
		kong.Name("clawdex"),
		kong.Description("Personal contact index backed by markdown and private Git."),
		kong.UsageOnError(),
		kong.Writers(stdout, stderr),
		kong.Vars{"version": Version},
		kong.Help(helpWithDiagnostics),
		kong.Exit(func(code int) {
			panic(kongExit{code: code})
		}),
	)
	if err != nil {
		return err
	}
	kctx, exitCode, exited, err := parseKong(parser, args)
	if exited {
		command := exitCommand(args)
		if exitCode != 0 {
			err = fmt.Errorf("%s exited with status %d", command, exitCode)
		}
		return finishStandaloneLog(command, stderr, jsonFlagPresent(args), verbosity, err)
	}
	if err != nil {
		err = usageErr{err}
		_ = finishStandaloneLog(commandFromArgs(args), stderr, jsonFlagPresent(args), verbosity, err)
		return err
	}
	command := logCommand(kctx.Command())
	// Runtime errors (config, command Run) route through the one crawlkit
	// envelope in --json mode. Kong parse errors are handled above and stay
	// plain text regardless of --json — kong prints them before Run and there
	// is no framework seam to intercept them, so that surface is unchanged.
	runErr := executeParsed(kctx, &root, stdout, stderr, verbosity, command)
	return ckoutput.WriteJSONErrorIfNeeded(stdout, root.JSON, runErr)
}

func executeParsed(kctx *kong.Context, root *CLI, stdout, stderr io.Writer, verbosity int, command string) error {
	configPath := repo.ResolveConfigPath(root.Config)
	cfg, err := repo.LoadConfig(configPath)
	if err != nil {
		return err
	}
	repoPath, err := repo.ResolveRepoPath(root.Repo, cfg)
	if err != nil {
		repoPath = cfg.RepoPath
	}
	r := &Runtime{
		ctx:        context.Background(),
		stdout:     stdout,
		stderr:     stderr,
		root:       root,
		command:    command,
		verbosity:  verbosity,
		configPath: configPath,
		cfg:        cfg,
		repo:       repo.Open(repoPath, cfg),
	}
	if err := r.startLogRun(command); err != nil {
		return err
	}
	r.store = index.NewWithLog(r.repo, indexLogWriter{r: r})
	kctx.Bind(r)
	return r.finishLogRun(kctx.Run(r))
}

type kongExit struct {
	code int
}

func parseKong(parser *kong.Kong, args []string) (ctx *kong.Context, exitCode int, exited bool, err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		if exit, ok := recovered.(kongExit); ok {
			exitCode = exit.code
			exited = true
			err = nil
			ctx = nil
			return
		}
		panic(recovered)
	}()
	ctx, err = parser.Parse(args)
	return ctx, 0, false, err
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var usage usageErr
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

type usageErr struct{ error }

// ErrorBody routes a usage error into the one crawlkit output envelope
// (rules §2.3/§2.4): the message stays machine-clean and the next step lives
// in remedy, never glued onto the message.
func (e usageErr) ErrorBody() ckoutput.ErrorBody {
	return ckoutput.ErrorBody{Code: "usage", Message: e.Error(), Remedy: "Run 'trawl contacts --help'."}
}

type InitCmd struct {
	Dir      string `arg:"" optional:"" help:"Contacts data repo directory"`
	Remote   string `name:"remote" help:"Git remote for contacts backup"`
	NoConfig bool   `name:"no-config" help:"Do not write app config"`
}

func (c *InitCmd) Run(r *Runtime) error {
	cfg := r.cfg
	if c.Dir != "" {
		cfg.RepoPath = c.Dir
	}
	if c.Remote != "" {
		cfg.Git.Remote = c.Remote
	}
	cfg.Normalize()
	dataRepo := repo.Open(cfg.RepoPath, cfg)
	if err := dataRepo.Init(r.ctx); err != nil {
		return err
	}
	if !c.NoConfig {
		if err := repo.WriteConfig(r.configPath, cfg); err != nil {
			return err
		}
	}
	return r.print(map[string]any{"repo_path": cfg.RepoPath, "remote": cfg.Git.Remote, "config_path": r.configPath})
}

type ConfigCmd struct {
	Show ConfigShowCmd `cmd:"" default:"1" help:"Show config"`
	Set  ConfigSetCmd  `cmd:"" help:"Set config value"`
}

type ConfigShowCmd struct{}

func (c *ConfigShowCmd) Run(r *Runtime) error {
	return r.print(r.cfg)
}

type ConfigSetCmd struct {
	Key   string `arg:"" help:"Config key"`
	Value string `arg:"" help:"Config value"`
}

func (c *ConfigSetCmd) Run(r *Runtime) error {
	cfg := r.cfg
	switch c.Key {
	case "repo_path":
		cfg.RepoPath = c.Value
	case "git.remote":
		cfg.Git.Remote = c.Value
	case "git.branch":
		cfg.Git.Branch = c.Value
	case "google.default_account":
		cfg.Google.DefaultAccount = c.Value
	default:
		return usageErr{fmt.Errorf("unsupported config key %q", c.Key)}
	}
	if r.root.DryRun {
		return r.print(cfg)
	}
	if err := repo.WriteConfig(r.configPath, cfg); err != nil {
		return err
	}
	return r.print(map[string]any{"config_path": r.configPath, "set": c.Key})
}

type PersonCmd struct {
	List PersonListCmd `cmd:"" help:"List people"`
	Show PersonShowCmd `cmd:"" help:"Show a person"`
}

type ContactsCmd struct {
	Export ContactsExportCmd `cmd:"" help:"Export contacts as JSON"`
}

type ContactsExportCmd struct{}

func (c *ContactsExportCmd) Run(r *Runtime) error {
	people, err := r.store.People()
	if err != nil {
		return err
	}
	return r.print(contactExportFromPeople(people))
}

type PersonListCmd struct {
	Query string `name:"query" short:"q" help:"Filter query"`
	Limit *int   `name:"limit" help:"Number of people to show (default 50)"`
	All   bool   `name:"all" help:"Show every person, no limit"`
}

func (c *PersonListCmd) Run(r *Runtime) error {
	limit, err := flags.Limit(limitOr(c.Limit, 50), c.Limit != nil, c.All)
	if err != nil {
		return usageErr{err}
	}
	people, err := r.store.People()
	if err != nil {
		return err
	}
	if c.Query != "" {
		filtered := people[:0]
		q := strings.ToLower(c.Query)
		for _, p := range people {
			if strings.Contains(strings.ToLower(p.Name+" "+p.ID+" "+strings.Join(p.Tags, " ")), q) {
				filtered = append(filtered, p)
			}
		}
		people = filtered
	}
	total := len(people)
	if limit > 0 && len(people) > limit {
		people = people[:limit]
	}
	if people == nil {
		people = []model.Person{}
	}
	return r.print(peopleEnvelope{
		Query:     c.Query,
		People:    people,
		Total:     total,
		Truncated: total > len(people),
		limit:     limit,
	})
}

type PersonShowCmd struct {
	Query string `arg:"" help:"ID, name, email, or phone"`
}

func (c *PersonShowCmd) Run(r *Runtime) error {
	p, err := r.store.FindPerson(c.Query)
	if err != nil {
		return err
	}
	return r.print(p)
}

type SearchCmd struct {
	Query string `arg:"" help:"Search query"`
	Limit *int   `name:"limit" help:"Number of results to return (default 20)"`
	All   bool   `name:"all" help:"Show every match, no limit"`
}

func (c *SearchCmd) Run(r *Runtime) error {
	limit, err := flags.Limit(limitOr(c.Limit, 20), c.Limit != nil, c.All)
	if err != nil {
		return usageErr{err}
	}
	hits, err := r.store.Search(c.Query)
	if err != nil {
		return err
	}
	total := len(hits)
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	if hits == nil {
		hits = []model.SearchHit{}
	}
	return r.print(searchEnvelope{
		Query:        c.Query,
		Results:      hits,
		TotalMatches: total,
		Truncated:    total > len(hits),
		limit:        limit,
	})
}

// limitOr returns the --limit value, or def when the flag was omitted. A nil
// pointer is kong's signal that the user did not pass --limit, which lets
// flags.Limit refuse --all combined with an explicit --limit.
func limitOr(limit *int, def int) int {
	if limit == nil {
		return def
	}
	return *limit
}

type ImportCmd struct {
	Apple    ImportAppleCmd    `cmd:"" help:"Import Apple Contacts into local markdown"`
	Birdclaw ImportBirdclawCmd `cmd:"" help:"Import X/Twitter DM contacts from local birdclaw archive"`
	Contacts ImportContactsCmd `cmd:"" help:"Import contacts from a source crawler"`
	Google   ImportGoogleCmd   `cmd:"" help:"Import Google Contacts into local markdown"`
	Discrawl ImportDiscrawlCmd `cmd:"" help:"Import Discord DM contacts from local discrawl archive"`
}

type ImportContactsCmd struct {
	From    string `name:"from" help:"Crawler binary to import contacts from"`
	FromAll bool   `name:"from-all" help:"Import contacts from every installed crawler that declares contacts export"`
}

type crawlerImportFailures []error

func (f crawlerImportFailures) Error() string {
	if len(f) == 1 {
		return "1 crawler import failed"
	}
	return fmt.Sprintf("%s crawler imports failed", render.FormatInteger(int64(len(f))))
}

func (f crawlerImportFailures) Unwrap() []error {
	return []error(f)
}

func (c *ImportContactsCmd) Run(r *Runtime) error {
	switch {
	case c.From != "" && c.FromAll:
		return usageErr{errors.New("use --from or --from-all, not both")}
	case c.From == "" && !c.FromAll:
		return usageErr{errors.New("provide --from CRAWLER or --from-all")}
	}
	var result importChangesEnvelope
	var failures crawlerImportFailures
	if c.From != "" {
		imported, skipped, err := r.importCrawlerContacts(c.From)
		if err != nil {
			return err
		}
		result.Changes = append(result.Changes, imported...)
		result.SkippedWithoutIdentifiers += skipped
	} else {
		binaries := discoverContactCrawlerBinaries(r.ctx)
		if len(binaries) == 0 {
			return errors.New("no installed crawler declares contacts export")
		}
		for _, binary := range binaries {
			imported, skipped, err := r.importCrawlerContacts(binary)
			if err != nil {
				failures = append(failures, err)
				if _, printErr := fmt.Fprintln(r.stderr, err); printErr != nil {
					return printErr
				}
				continue
			}
			result.Changes = append(result.Changes, imported...)
			result.SkippedWithoutIdentifiers += skipped
		}
	}
	if err := r.print(newImportChangesEnvelope(result.Changes, result.SkippedWithoutIdentifiers)); err != nil {
		return err
	}
	if len(failures) > 0 {
		return failures
	}
	return nil
}

func (r *Runtime) importCrawlerContacts(binary string) ([]model.ImportChange, int, error) {
	source, contacts, skipped, err := readCrawlerContacts(r.ctx, binary)
	if err != nil {
		return nil, 0, err
	}
	changes, err := r.store.ImportCrawlerContacts(source, contacts, r.root.DryRun, time.Now())
	return changes, skipped, err
}

func readCrawlerContacts(ctx context.Context, binary string) (string, []model.SourceContact, int, error) {
	manifest, err := readCrawlerManifest(ctx, binary)
	if err != nil {
		return "", nil, 0, err
	}
	argv := contactExportArgv(binary)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- binary is an explicit local crawler command and no shell is used.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", nil, 0, fmt.Errorf("%s contacts export failed: %w: %s", binary, err, msg)
		}
		return "", nil, 0, fmt.Errorf("%s contacts export failed: %w", binary, err)
	}
	export, err := contactexport.Decode(bytes.NewReader(data))
	if err != nil {
		return "", nil, 0, fmt.Errorf("%s contacts export decode failed: %w", binary, err)
	}
	source := strings.TrimSpace(manifest.ID)
	if source == "" {
		source = filepath.Base(binary)
	}
	return source, sourceContactsFromExport(source, export), export.SkippedWithoutIdentifiers, nil
}

func contactsExportCommand(manifest control.Manifest) (control.Command, bool) {
	for _, name := range []string{"contact-export", "contacts_export"} {
		command, ok := manifest.Commands[name]
		if ok {
			return command, true
		}
	}
	for _, command := range manifest.Commands {
		if commandDeclaresContactsExport(command) {
			return command, true
		}
	}
	return control.Command{}, false
}

func commandDeclaresContactsExport(command control.Command) bool {
	return len(command.Argv) > 0 && slices.Contains(command.Argv, "--json") && commandRunsContactsExport(command)
}

func commandRunsContactsExport(command control.Command) bool {
	if len(command.Argv) == 0 {
		return false
	}
	seenContacts := false
	for _, arg := range command.Argv {
		switch arg {
		case "contacts":
			seenContacts = true
		case "export":
			if seenContacts {
				return true
			}
		}
	}
	return false
}

func validateContactsExportCommand(binary string, manifest control.Manifest) error {
	command, ok := contactsExportCommand(manifest)
	if !ok {
		return nil
	}
	if len(command.Argv) == 0 {
		return fmt.Errorf("%s contacts export command must have argv", binary)
	}
	if !command.JSON {
		return fmt.Errorf("%s contacts export must advertise json output", binary)
	}
	if command.Mutates {
		return fmt.Errorf("%s contacts export must be read-only", binary)
	}
	if !slices.Contains(command.Argv, "--json") {
		return fmt.Errorf("%s contacts export command must include --json", binary)
	}
	if !commandRunsContactsExport(command) {
		return fmt.Errorf("%s contacts export command must run contacts export", binary)
	}
	return nil
}

func contactExportArgv(binary string) []string {
	return []string{binary, "contacts", "export", "--json"}
}

func discoverContactCrawlerBinaries(ctx context.Context) []string {
	var binaries []string
	seen := map[string]bool{}
	// This fixed list only probes crawlers with a reviewed contacts export contract.
	for _, name := range []string{"imsgcrawl", "telecrawl", "wacrawl", "gogcrawl", "calcrawl"} {
		path, err := exec.LookPath(name)
		if err != nil || seen[path] {
			continue
		}
		if _, err := readCrawlerManifest(ctx, path); err != nil {
			continue
		}
		binaries = append(binaries, path)
		seen[path] = true
	}
	sort.Strings(binaries)
	return binaries
}

func readCrawlerManifest(ctx context.Context, binary string) (control.Manifest, error) {
	cmd := exec.CommandContext(ctx, binary, "metadata", "--json") // #nosec G204 -- binary is an explicit local crawler command.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return control.Manifest{}, fmt.Errorf("%s metadata failed: %w: %s", binary, err, msg)
		}
		return control.Manifest{}, fmt.Errorf("%s metadata failed: %w", binary, err)
	}
	var manifest control.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return control.Manifest{}, fmt.Errorf("%s metadata decode failed: %w", binary, err)
	}
	if manifest.SchemaVersion != control.RunnerManifestVersion {
		return control.Manifest{}, fmt.Errorf("%s metadata schema_version = %d, want %d", binary, manifest.SchemaVersion, control.RunnerManifestVersion)
	}
	if manifest.ContractVersion != control.ContractVersion {
		return control.Manifest{}, fmt.Errorf("%s metadata contract_version = %d, want %d", binary, manifest.ContractVersion, control.ContractVersion)
	}
	if !slices.Contains(manifest.Capabilities, "contacts_export") {
		return control.Manifest{}, fmt.Errorf("%s metadata does not declare contacts_export capability", binary)
	}
	if err := validateSchemaV2CommandFields(binary, data); err != nil {
		return control.Manifest{}, err
	}
	if err := validateContactsExportCommand(binary, manifest); err != nil {
		return control.Manifest{}, err
	}
	return manifest, nil
}

func validateSchemaV2CommandFields(binary string, data []byte) error {
	var raw struct {
		SchemaVersion int                                   `json:"schema_version"`
		Commands      map[string]map[string]json.RawMessage `json:"commands"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.SchemaVersion < 2 {
		return nil
	}
	if len(raw.Commands) == 0 {
		return fmt.Errorf("%s metadata schema v2 commands are missing", binary)
	}
	for name, command := range raw.Commands {
		if err := validateSchemaV2Command(binary, name, command); err != nil {
			return err
		}
	}
	return nil
}

func validateSchemaV2Command(binary, name string, command map[string]json.RawMessage) error {
	var argv []string
	if raw, ok := command["argv"]; !ok {
		return fmt.Errorf("%s command %q argv is missing", binary, name)
	} else if err := json.Unmarshal(raw, &argv); err != nil || len(argv) == 0 {
		return fmt.Errorf("%s command %q argv is missing or empty", binary, name)
	}
	for i, arg := range argv {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("%s command %q argv %d is empty", binary, name, i)
		}
	}
	if raw, ok := command["json"]; !ok {
		return fmt.Errorf("%s command %q json is missing", binary, name)
	} else if !isJSONBool(raw) {
		return fmt.Errorf("%s command %q json is not a boolean", binary, name)
	}
	if raw, ok := command["mutates"]; !ok {
		return fmt.Errorf("%s command %q mutates is missing", binary, name)
	} else if !isJSONBool(raw) {
		return fmt.Errorf("%s command %q mutates is not a boolean", binary, name)
	}
	return nil
}

func isJSONBool(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))
}

func sourceContactsFromExport(source string, export contactexport.ContactExport) []model.SourceContact {
	contacts := make([]model.SourceContact, 0, len(export.Contacts))
	for _, c := range export.Contacts {
		contact := model.SourceContact{Source: source, Name: c.DisplayName}
		for i, phone := range c.PhoneNumbers {
			contact.Phones = append(contact.Phones, model.ContactValue{Value: phone, Source: source, Primary: i == 0})
		}
		for i, email := range c.Emails {
			contact.Emails = append(contact.Emails, model.ContactValue{Value: email, Source: source, Primary: i == 0 && len(contact.Phones) == 0})
		}
		for i, address := range c.Addresses {
			contact.Addresses = append(contact.Addresses, model.ContactValue{Value: address, Label: "other", Source: source, Primary: i == 0})
		}
		contact.Accounts = mergeCrawlerExportAccounts(c.Accounts, c.Handles)
		contacts = append(contacts, contact)
	}
	return contacts
}

func contactExportFromPeople(people []model.Person) contactexport.ContactExport {
	out := contactexport.ContactExport{Contacts: make([]contactexport.Contact, 0, len(people))}
	for _, person := range people {
		contact := contactexport.Contact{
			DisplayName: person.Name,
			Accounts:    person.Accounts,
		}
		for _, phone := range person.Phones {
			if strings.TrimSpace(phone.Value) != "" {
				contact.PhoneNumbers = append(contact.PhoneNumbers, phone.Value)
			}
		}
		for _, email := range person.Emails {
			if strings.TrimSpace(email.Value) != "" {
				contact.Emails = append(contact.Emails, email.Value)
			}
		}
		for _, address := range person.Addresses {
			if strings.TrimSpace(address.Value) != "" {
				contact.Addresses = append(contact.Addresses, address.Value)
			}
		}
		out.Contacts = append(out.Contacts, contact)
	}
	return out
}

func mergeCrawlerExportAccounts(values ...map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, accounts := range values {
		for service, handles := range accounts {
			service = strings.TrimSpace(strings.ToLower(service))
			if service == "" {
				continue
			}
			for _, handle := range handles {
				handle = strings.TrimSpace(handle)
				if handle == "" {
					continue
				}
				if !slices.Contains(out[service], handle) {
					out[service] = append(out[service], handle)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type ImportAppleCmd struct {
	Input   string `name:"input" help:"JSON/NDJSON contact file instead of macOS Contacts"`
	Avatars bool   `name:"avatars" help:"Import local avatar thumbnails"`
}

func (c *ImportAppleCmd) Run(r *Runtime) error {
	var contacts []apple.Contact
	var err error
	if c.Input != "" {
		contacts, err = apple.ReadFile(c.Input)
	} else {
		contacts, err = apple.ReadSystem(r.ctx)
	}
	if err != nil {
		return err
	}
	changes, err := r.store.ImportContacts("apple", apple.ToSourceContacts(contacts, c.Avatars), r.root.DryRun, time.Now())
	if err != nil {
		return err
	}
	return r.print(newImportChangesEnvelope(changes, 0))
}

type ImportGoogleCmd struct {
	Account string `name:"account" help:"Google account email"`
	Avatars bool   `name:"avatars" help:"Fetch Google contact avatar bytes through gog raw photo URLs"`
}

func (c *ImportGoogleCmd) Run(r *Runtime) error {
	account := c.Account
	if account == "" {
		account = r.cfg.Google.DefaultAccount
	}
	contacts, err := (google.GogAdapter{}).ListContactsWithOptions(r.ctx, account, google.Options{IncludeAvatars: c.Avatars})
	if err != nil {
		return err
	}
	changes, err := r.store.ImportContacts("google", contacts, r.root.DryRun, time.Now())
	if err != nil {
		return err
	}
	return r.print(newImportChangesEnvelope(changes, 0))
}

type ImportDiscrawlCmd struct {
	DBPath      string `name:"db" help:"discrawl SQLite database path" default:"~/.discrawl/discrawl.db"`
	MinMessages int    `name:"min-messages" help:"Import DMs with more than this many messages" default:"4"`
}

type ImportBirdclawCmd struct {
	DBPath      string `name:"db" help:"birdclaw SQLite database path" default:"~/.birdclaw/birdclaw.sqlite"`
	MinMessages int    `name:"min-messages" help:"Import DMs with more than this many messages" default:"4"`
}

func (c *ImportBirdclawCmd) Run(r *Runtime) error {
	contacts, err := (birdclaw.Adapter{DBPath: c.DBPath}).ListDMContacts(r.ctx, c.MinMessages)
	if err != nil {
		return err
	}
	changes, err := r.store.ImportContacts("x", contacts, r.root.DryRun, time.Now())
	if err != nil {
		return err
	}
	return r.print(newImportChangesEnvelope(changes, 0))
}

func (c *ImportDiscrawlCmd) Run(r *Runtime) error {
	contacts, err := (discrawl.Adapter{DBPath: c.DBPath}).ListDMContacts(r.ctx, c.MinMessages)
	if err != nil {
		return err
	}
	changes, err := r.store.ImportContacts("discord", contacts, r.root.DryRun, time.Now())
	if err != nil {
		return err
	}
	return r.print(newImportChangesEnvelope(changes, 0))
}

type SyncCmd struct {
	Apple  SyncAppleCmd  `cmd:"" help:"Preview Apple Contacts sync"`
	Google SyncGoogleCmd `cmd:"" help:"Preview Google Contacts sync"`
}

type SyncAppleCmd struct{}

func (c *SyncAppleCmd) Run(r *Runtime) (err error) {
	started := time.Now()
	defer func() {
		if err == nil {
			r.logSyncTimings("apple", time.Since(started))
		}
	}()
	return r.print(map[string]any{"dry_run": true, "status": "remote writes not implemented yet; use import apple for local markdown projection"})
}

type SyncGoogleCmd struct {
	Account string `name:"account" help:"Google account email"`
}

func (c *SyncGoogleCmd) Run(r *Runtime) (err error) {
	started := time.Now()
	defer func() {
		if err == nil {
			r.logSyncTimings("google", time.Since(started))
		}
	}()
	return r.print(map[string]any{"dry_run": true, "account": firstNonEmpty(c.Account, r.cfg.Google.DefaultAccount), "status": "remote writes not implemented yet; use import google for local markdown projection"})
}

type ExportCmd struct {
	VCard ExportVCardCmd `cmd:"" name:"vcard" help:"Export vCard"`
}

type ExportVCardCmd struct {
	Person         string `name:"person" help:"Person query"`
	All            bool   `name:"all" help:"Export all people"`
	IncludeAvatars bool   `name:"include-avatars" help:"Include avatar PHOTO fields"`
	Out            string `name:"out" short:"o" required:"" help:"Output .vcf path, or - for stdout"`
}

func (c *ExportVCardCmd) Run(r *Runtime) error {
	var people []model.Person
	switch {
	case c.All:
		var err error
		people, err = r.store.People()
		if err != nil {
			return err
		}
	case c.Person != "":
		p, err := r.store.FindPerson(c.Person)
		if err != nil {
			return err
		}
		people = []model.Person{p}
	default:
		return usageErr{errors.New("provide --person or --all")}
	}
	if c.Out == "-" {
		return vcard.WriteWithOptions(r.stdout, people, vcard.Options{IncludeAvatars: c.IncludeAvatars})
	}
	f, err := os.Create(c.Out)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := vcard.WriteWithOptions(f, people, vcard.Options{IncludeAvatars: c.IncludeAvatars}); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return r.print(map[string]any{"exported": len(people), "out": c.Out})
}

type GitCmd struct {
	Status GitStatusCmd `cmd:"" default:"1" help:"Show git status"`
	Pull   GitPullCmd   `cmd:"" help:"Pull data repo"`
	Push   GitPushCmd   `cmd:"" help:"Push data repo"`
	Commit GitCommitCmd `cmd:"" help:"Commit data repo changes"`
}

type GitStatusCmd struct{}

func (c *GitStatusCmd) Run(r *Runtime) error {
	// #nosec G204 -- git is fixed and repo path is passed as a plain argument.
	cmd := exec.CommandContext(r.ctx, "git", "-C", r.repo.Path, "status", "--short", "--branch")
	cmd.Stdout = r.stdout
	cmd.Stderr = r.stderr
	return cmd.Run()
}

type GitPullCmd struct{}

func (c *GitPullCmd) Run(r *Runtime) error {
	return r.repo.Pull(r.ctx)
}

type GitPushCmd struct{}

func (c *GitPushCmd) Run(r *Runtime) error {
	return r.repo.Push(r.ctx)
}

type GitCommitCmd struct {
	Message string `name:"message" short:"m" help:"Commit message" default:"sync: update contacts"`
}

func (c *GitCommitCmd) Run(r *Runtime) error {
	committed, err := r.repo.Commit(r.ctx, c.Message)
	if err != nil {
		return err
	}
	return r.print(map[string]any{"committed": committed})
}

type DoctorCmd struct {
	Repair bool `name:"repair" help:"Repair damaged markdown frontmatter"`
}

func (c *DoctorCmd) Run(r *Runtime) error {
	// Contract JSON diagnostics are read-only; --repair keeps clawdex's legacy repair report.
	if r.root.JSON && !c.Repair {
		return r.print(r.doctorReport())
	}
	if !c.Repair {
		return r.printDoctorReport(r.doctorReport())
	}
	store := r.store
	store.Repo.Config.Repair.AutoRepair = false
	people, err := store.People()
	if err != nil {
		return err
	}
	dirty, _ := r.repo.Dirty(r.ctx)
	result := map[string]any{
		"config_path": r.configPath,
		"repo_path":   r.repo.Path,
		"remote":      r.cfg.Git.Remote,
		"people":      len(people),
		"git_dirty":   dirty,
	}
	avatarProblems := 0
	for _, p := range people {
		avatarProblems += len(avatar.Validate(p))
	}
	if avatarProblems > 0 {
		result["avatar_problems"] = avatarProblems
	}
	if c.Repair {
		var repaired int
		var avatarRepaired int
		for _, p := range people {
			loaded, report, err := markdown.ReadPerson(p.Path)
			if err != nil {
				return err
			}
			if report.Needed {
				repaired++
				if !r.root.DryRun {
					if err := markdown.RepairPerson(p.Path, r.repo.RepairDir(), loaded, report, r.cfg.Repair.BackupBeforeRepair); err != nil {
						return err
					}
				}
			}
			if len(avatar.Validate(loaded)) > 0 {
				avatarRepaired++
				if !r.root.DryRun {
					_, _, err := store.RepairAvatarMetadata(loaded, time.Now())
					if err != nil {
						return err
					}
				}
			}
		}
		result["repaired"] = repaired
		result["avatar_repaired"] = avatarRepaired
		result["dry_run"] = r.root.DryRun
	}
	if r.root.JSON {
		return r.print(result)
	}
	return r.printDoctorRepairResult(result)
}

func (r *Runtime) printDoctorRepairResult(result map[string]any) error {
	people := intFromResult(result, "people")
	repaired := intFromResult(result, "repaired")
	avatarProblems := intFromResult(result, "avatar_problems")
	avatarRepaired := intFromResult(result, "avatar_repaired")
	dryRun := boolFromResult(result, "dry_run")

	checks := []render.Check{{
		Name:    "Contacts repo",
		State:   render.CheckOK,
		Message: peopleMessage(people),
	}, {
		Name:    "Markdown repair",
		State:   repairCheckState(repaired),
		Message: repairMessage("person markdown file", repaired, dryRun),
	}}
	if avatarProblems > 0 || avatarRepaired > 0 {
		checks = append(checks, render.Check{
			Name:    "Avatar metadata",
			State:   repairCheckState(avatarRepaired),
			Message: repairMessage("avatar metadata entry", avatarRepaired, dryRun),
		})
	}
	return render.WriteDoctor(r.stdout, checks, r.renderLogTail())
}

func intFromResult(result map[string]any, key string) int {
	switch value := result[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func boolFromResult(result map[string]any, key string) bool {
	value, _ := result[key].(bool)
	return value
}

func repairCheckState(count int) render.CheckState {
	if count == 0 {
		return render.CheckEmpty
	}
	return render.CheckOK
}

func peopleMessage(count int) string {
	if count == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%s people", render.FormatInteger(int64(count)))
}

func repairMessage(item string, count int, dryRun bool) string {
	if count == 0 {
		return fmt.Sprintf("no %s needed repair", pluralItem(item))
	}
	action := "repaired"
	if dryRun {
		action = "would be repaired"
	}
	if count == 1 {
		return fmt.Sprintf("1 %s %s", item, action)
	}
	return fmt.Sprintf("%s %s %s", render.FormatInteger(int64(count)), pluralItem(item), action)
}

func pluralItem(item string) string {
	if strings.HasSuffix(item, "entry") {
		return strings.TrimSuffix(item, "entry") + "entries"
	}
	return item + "s"
}

func countNoun(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%s %s", render.FormatInteger(int64(count)), plural)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
