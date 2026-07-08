package crawlkit

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/openclaw/crawlkit/output"
)

var spineVerbKeys = map[string]struct{}{
	"metadata":        {},
	"status":          {},
	"doctor":          {},
	"sync":            {},
	"search":          {},
	"open":            {},
	"who":             {},
	"contacts_export": {},
}

type spineVerbError struct {
	message string
	remedy  string
}

func (e spineVerbError) Error() string {
	return e.message
}

func (e spineVerbError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{
		Code:    "invalid_spine_verb",
		Message: e.Error(),
		Remedy:  e.remedy,
	}
}

func spineVerbDeclarations(source Crawler) (map[string]Verb, error) {
	decls := map[string]Verb{}
	for _, verb := range source.Verbs() {
		key, ok := spineVerbKey(verb.Name)
		if !ok {
			continue
		}
		if err := validateSpineVerb(key, verb, decls, unsupportedSpineVerbError(source, key)); err != nil {
			return nil, err
		}
		decls[key] = verb
	}
	return decls, nil
}

func supportedSpineVerbDeclarations(source Crawler) (map[string]Verb, error) {
	decls := map[string]Verb{}
	for _, verb := range source.Verbs() {
		key, ok := spineVerbKey(verb.Name)
		if !ok {
			continue
		}
		if unsupportedSpineInterface(source, key) != "" {
			continue
		}
		if err := validateSpineVerb(key, verb, decls, nil); err != nil {
			return nil, err
		}
		decls[key] = verb
	}
	return decls, nil
}

func spineDeclaration(spine map[string]Verb, key string) *Verb {
	verb, ok := spine[key]
	if !ok {
		return nil
	}
	return &verb
}

func validateSpineVerb(key string, verb Verb, decls map[string]Verb, supportErr error) error {
	if fields := invalidSpineVerbFields(verb); len(fields) > 0 {
		return invalidSpineVerbFieldsError(key, fields)
	}
	if supportErr != nil {
		return supportErr
	}
	if _, ok := decls[key]; ok {
		return duplicateSpineVerbError(key)
	}
	if collisions := spineFlagCollisions(key, declaredFlagNames(verb)); len(collisions) > 0 {
		return spineFlagCollisionVerbError(key, collisions)
	}
	if err := validateSpineVerbStore(key, verb.Store); err != nil {
		return err
	}
	return nil
}

func unsupportedSpineVerbError(source Crawler, key string) error {
	if interfaceName := unsupportedSpineInterface(source, key); interfaceName != "" {
		return unsupportedSpineInterfaceError(key, interfaceName)
	}
	return nil
}

func invalidSpineVerbFieldsError(key string, fields []string) spineVerbError {
	return spineVerbError{
		message: fmt.Sprintf("invalid %s Verb declaration: spine verb declarations may only set Name, Flags, and Store", key),
		remedy:  fmt.Sprintf("Remove %s from the %s Verb declaration.", humanList(fields), key),
	}
}

func invalidSpineVerbStoreError(key string, declared StoreAccess) spineVerbError {
	return spineVerbError{
		message: fmt.Sprintf("invalid %s Verb declaration: %s is not valid; %s", key, storeAccessName(declared), spineVerbStoreAllowance(key)),
		remedy:  spineVerbStoreRemedy(key),
	}
}

func unsupportedSpineInterfaceError(key, interfaceName string) spineVerbError {
	return spineVerbError{
		message: fmt.Sprintf("invalid %s Verb declaration: source does not implement %s", key, interfaceName),
		remedy:  fmt.Sprintf("Implement crawlkit.%s or remove the %s Verb declaration.", interfaceName, key),
	}
}

func duplicateSpineVerbError(key string) spineVerbError {
	return spineVerbError{
		message: fmt.Sprintf("invalid %s Verb declaration: declared more than once", key),
		remedy:  fmt.Sprintf("Keep one %s Verb declaration and remove the duplicate.", key),
	}
}

func spineFlagCollisionVerbError(key string, flags []string) spineVerbError {
	return spineVerbError{
		message: fmt.Sprintf("invalid %s Verb declaration: crawler flag %s collides with a runner-owned flag", key, humanList(flags)),
		remedy:  fmt.Sprintf("Remove %s from the %s Verb declaration; the runner owns that flag.", humanList(flags), key),
	}
}

func spineVerbKey(name string) (string, bool) {
	key := commandKey(name)
	_, ok := spineVerbKeys[key]
	return key, ok
}

func invalidSpineVerbFields(verb Verb) []string {
	var fields []string
	if strings.TrimSpace(verb.Help) != "" {
		fields = append(fields, "Help")
	}
	if verb.Run != nil {
		fields = append(fields, "Run")
	}
	if verb.Mutates {
		fields = append(fields, "Mutates")
	}
	if verb.Timeout != 0 {
		fields = append(fields, "Timeout")
	}
	if len(verb.Args) > 0 {
		fields = append(fields, "Args")
	}
	return fields
}

func unsupportedSpineInterface(source Crawler, key string) string {
	switch key {
	case "metadata", "status", "doctor":
		return ""
	case "sync":
		if _, ok := source.(Syncer); !ok {
			return "Syncer"
		}
	case "search":
		if _, ok := source.(Searcher); !ok {
			return "Searcher"
		}
	case "open":
		if _, ok := source.(Opener); !ok {
			return "Opener"
		}
	case "who":
		if _, ok := source.(WhoMatcher); !ok {
			return "WhoMatcher"
		}
	case "contacts_export":
		if _, ok := source.(ContactExporter); !ok {
			return "ContactExporter"
		}
	}
	return ""
}

func validateSpineVerbStore(key string, declared StoreAccess) error {
	if declared == StoreDefault {
		return nil
	}
	switch spineDefaultStoreMode(key) {
	case storeRead:
		if declared == StoreNone || declared == StoreOptional {
			return nil
		}
	case storeOptional:
		if declared == StoreNone {
			return nil
		}
	}
	return invalidSpineVerbStoreError(key, declared)
}

func spineStoreMode(key string, verb *Verb) storeMode {
	if verb == nil || verb.Store == StoreDefault {
		return spineDefaultStoreMode(key)
	}
	switch verb.Store {
	case StoreNone:
		return storeNone
	case StoreOptional:
		return storeOptional
	default:
		return spineDefaultStoreMode(key)
	}
}

func spineDefaultStoreMode(key string) storeMode {
	switch key {
	case "metadata":
		return storeNone
	case "status", "doctor":
		return storeOptional
	case "sync":
		return storeWrite
	case "search", "open", "who", "contacts_export":
		return storeRead
	default:
		return storeRead
	}
}

func spineVerbStoreRemedy(key string) string {
	switch spineDefaultStoreMode(key) {
	case storeRead:
		return fmt.Sprintf("Remove Store from the %s Verb declaration, or set Store to StoreNone or StoreOptional.", key)
	case storeOptional:
		return fmt.Sprintf("Remove Store from the %s Verb declaration, or set Store to StoreNone.", key)
	case storeWrite:
		return fmt.Sprintf("Remove Store from the %s Verb declaration; %s always writes the archive.", key, key)
	default:
		return fmt.Sprintf("Remove Store from the %s Verb declaration.", key)
	}
}

func spineVerbStoreAllowance(key string) string {
	switch spineDefaultStoreMode(key) {
	case storeRead:
		return "use StoreNone or StoreOptional"
	case storeOptional:
		return "use StoreNone"
	case storeWrite:
		return key + " always writes the archive"
	default:
		return "remove Store"
	}
}

func declaredFlagNames(verb Verb) []string {
	fs := flag.NewFlagSet(verb.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if verb.Flags != nil {
		verb.Flags(fs)
	}
	var names []string
	fs.VisitAll(func(f *flag.Flag) {
		names = append(names, f.Name)
	})
	sort.Strings(names)
	return names
}

func spineFlagCollisions(key string, names []string) []string {
	owned := runnerOwnedSpineFlags(key)
	if len(owned) == 0 {
		return nil
	}
	var collisions []string
	for _, name := range names {
		if _, ok := owned[name]; ok {
			collisions = append(collisions, "--"+name)
		}
	}
	return collisions
}

func runnerOwnedSpineFlags(key string) map[string]struct{} {
	owned := runnerOwnedGlobalFlagNames()
	if key == "search" {
		for name := range runnerOwnedSearchFlagNames() {
			owned[name] = struct{}{}
		}
	}
	return owned
}

func parseSpineFlags(verb Verb, args []string, keepDelimiter bool) ([]string, error) {
	if verb.Flags == nil {
		return append([]string(nil), args...), nil
	}
	fs := flag.NewFlagSet(verb.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	verb.Flags(fs)
	flagArgs, rest, err := spineFlagArgs(fs, args, keepDelimiter)
	if err != nil {
		return nil, err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, output.UsageError{Err: err}
	}
	return rest, nil
}

func spineFlagArgs(fs *flag.FlagSet, args []string, keepDelimiter bool) ([]string, []string, error) {
	var flagArgs []string
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if keepDelimiter {
				rest = append(rest, args[i:]...)
			} else {
				rest = append(rest, args[i+1:]...)
			}
			break
		}
		if !strings.HasPrefix(arg, "-") {
			rest = append(rest, arg)
			continue
		}
		name, value, inline := splitFlagValue(arg)
		fl := fs.Lookup(strings.TrimLeft(name, "-"))
		if fl == nil {
			rest = append(rest, arg)
			continue
		}
		if inline {
			flagArgs = append(flagArgs, name+"="+value)
			continue
		}
		flagArgs = append(flagArgs, name)
		if isBoolFlag(fl) {
			continue
		}
		i++
		if i >= len(args) {
			return nil, nil, output.UsageError{Err: fmt.Errorf("flag needs an argument: %s", name)}
		}
		flagArgs = append(flagArgs, args[i])
	}
	return flagArgs, rest, nil
}

func humanList(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " and " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", and " + values[len(values)-1]
	}
}
