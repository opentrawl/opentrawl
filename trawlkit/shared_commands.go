package trawlkit

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

var sharedTrawlerCommandNames = map[string]struct{}{
	"metadata":      {},
	"status":        {},
	"sync":          {},
	"search":        {},
	"open":          {},
	"who":           {},
	"conversations": {},
	"messages":      {},
}

type sharedTrawlerCommandError struct {
	message string
}

func (e sharedTrawlerCommandError) Error() string {
	return e.message
}

func (e sharedTrawlerCommandError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    "invalid_trawler_command_declaration",
		Message: e.Error(),
	}
}

func sharedTrawlerCommandDeclarations(source Trawler) (map[string]TrawlerCommand, error) {
	decls := map[string]TrawlerCommand{}
	for _, command := range source.TrawlerCommands() {
		key, ok := sharedTrawlerCommandName(command.TrawlerCommandName)
		if !ok {
			continue
		}
		if err := validateSharedTrawlerCommand(key, command, decls, unsupportedSharedTrawlerCommandError(source, key)); err != nil {
			return nil, err
		}
		decls[key] = command
	}
	return decls, nil
}

func supportedSharedTrawlerCommandDeclarations(source Trawler) (map[string]TrawlerCommand, error) {
	decls := map[string]TrawlerCommand{}
	for _, command := range source.TrawlerCommands() {
		key, ok := sharedTrawlerCommandName(command.TrawlerCommandName)
		if !ok {
			continue
		}
		if unsupportedSharedTrawlerCommandInterface(source, key) != "" {
			continue
		}
		if err := validateSharedTrawlerCommand(key, command, decls, nil); err != nil {
			return nil, err
		}
		decls[key] = command
	}
	return decls, nil
}

func sharedTrawlerCommandDeclaration(sharedCommands map[string]TrawlerCommand, key string) *TrawlerCommand {
	command, ok := sharedCommands[key]
	if !ok {
		return nil
	}
	return &command
}

func validateSharedTrawlerCommand(key string, command TrawlerCommand, declarations map[string]TrawlerCommand, supportError error) error {
	if fields := invalidSharedTrawlerCommandFields(command); len(fields) > 0 {
		return invalidSharedTrawlerCommandFieldsError(key, fields)
	}
	if supportError != nil {
		return supportError
	}
	if _, ok := declarations[key]; ok {
		return duplicateSharedTrawlerCommandError(key)
	}
	if collisions := sharedTrawlerCommandFlagCollisions(key, declaredFlagNames(command)); len(collisions) > 0 {
		return sharedTrawlerCommandFlagCollisionError(key, collisions)
	}
	if err := validateSharedTrawlerCommandArchiveAccess(key, command.TrawlerCommandArchiveAccess); err != nil {
		return err
	}
	return nil
}

func unsupportedSharedTrawlerCommandError(source Trawler, key string) error {
	if interfaceName := unsupportedSharedTrawlerCommandInterface(source, key); interfaceName != "" {
		return unsupportedSharedTrawlerCommandInterfaceError(key, interfaceName)
	}
	return nil
}

func invalidSharedTrawlerCommandFieldsError(key string, fields []string) sharedTrawlerCommandError {
	return sharedTrawlerCommandError{
		message: fmt.Sprintf("invalid %s TrawlerCommand declaration: shared command declarations may only set TrawlerCommandName, RegisterTrawlerCommandFlags, TrawlerCommandArchiveAccess, and TrawlerCommandHelpListing", key),
	}
}

func invalidSharedTrawlerCommandArchiveAccessError(key string, declared TrawlerCommandArchiveAccess) sharedTrawlerCommandError {
	return sharedTrawlerCommandError{
		message: fmt.Sprintf("invalid %s TrawlerCommand declaration: %s is not valid; %s", key, trawlerCommandArchiveAccessName(declared), sharedTrawlerCommandArchiveAccessAllowance(key)),
	}
}

func unsupportedSharedTrawlerCommandInterfaceError(key, interfaceName string) sharedTrawlerCommandError {
	return sharedTrawlerCommandError{
		message: fmt.Sprintf("invalid %s TrawlerCommand declaration: trawler does not implement %s", key, interfaceName),
	}
}

func duplicateSharedTrawlerCommandError(key string) sharedTrawlerCommandError {
	return sharedTrawlerCommandError{
		message: fmt.Sprintf("invalid %s TrawlerCommand declaration: declared more than once", key),
	}
}

func sharedTrawlerCommandFlagCollisionError(key string, flags []string) sharedTrawlerCommandError {
	return sharedTrawlerCommandError{
		message: fmt.Sprintf("invalid %s TrawlerCommand declaration: trawler flag %s collides with a runner-owned flag", key, humanList(flags)),
	}
}

func sharedTrawlerCommandName(name string) (string, bool) {
	key := commandKey(name)
	_, ok := sharedTrawlerCommandNames[key]
	return key, ok
}

func invalidSharedTrawlerCommandFields(command TrawlerCommand) []string {
	var fields []string
	if strings.TrimSpace(command.TrawlerCommandHelpDescription) != "" {
		fields = append(fields, "TrawlerCommandHelpDescription")
	}
	if command.ExecuteTrawlerCommand != nil {
		fields = append(fields, "ExecuteTrawlerCommand")
	}
	if command.TrawlerCommandChangesArchive {
		fields = append(fields, "TrawlerCommandChangesArchive")
	}
	if command.TrawlerCommandMaximumExecutionTime != 0 {
		fields = append(fields, "TrawlerCommandMaximumExecutionTime")
	}
	if len(command.TrawlerCommandPositionalArgumentNames) > 0 {
		fields = append(fields, "TrawlerCommandPositionalArgumentNames")
	}
	return fields
}

func unsupportedSharedTrawlerCommandInterface(source Trawler, key string) string {
	switch key {
	case "metadata", "status":
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
		if _, ok := source.(RecordOpener); !ok {
			return "RecordOpener"
		}
	case "who":
		if _, ok := source.(WhoMatcher); !ok {
			return "WhoMatcher"
		}
	case "conversations":
		if _, ok := source.(ConversationLister); !ok {
			return "ConversationLister"
		}
	case "messages":
		if _, ok := source.(TrawlerMessageLister); !ok {
			return "TrawlerMessageLister"
		}
	}
	return ""
}

func validateSharedTrawlerCommandArchiveAccess(key string, declared TrawlerCommandArchiveAccess) error {
	if declared == TrawlerCommandArchiveAccessDefault {
		return nil
	}
	switch defaultSharedTrawlerCommandArchiveAccessMode(key) {
	case storeRead:
		if declared == TrawlerCommandArchiveAccessNone || declared == TrawlerCommandArchiveAccessOptional {
			return nil
		}
	case storeOptional:
		if declared == TrawlerCommandArchiveAccessNone {
			return nil
		}
	}
	return invalidSharedTrawlerCommandArchiveAccessError(key, declared)
}

func sharedTrawlerCommandArchiveAccessMode(key string, command *TrawlerCommand) storeMode {
	if command == nil || command.TrawlerCommandArchiveAccess == TrawlerCommandArchiveAccessDefault {
		return defaultSharedTrawlerCommandArchiveAccessMode(key)
	}
	switch command.TrawlerCommandArchiveAccess {
	case TrawlerCommandArchiveAccessNone:
		return storeNone
	case TrawlerCommandArchiveAccessOptional:
		return storeOptional
	default:
		return defaultSharedTrawlerCommandArchiveAccessMode(key)
	}
}

func defaultSharedTrawlerCommandArchiveAccessMode(key string) storeMode {
	switch key {
	case "metadata":
		return storeNone
	case "status":
		return storeOptional
	case "sync":
		return storeWrite
	case "search", "open", "who", "conversations", "messages":
		return storeRead
	default:
		return storeRead
	}
}

func sharedTrawlerCommandArchiveAccessAllowance(key string) string {
	switch defaultSharedTrawlerCommandArchiveAccessMode(key) {
	case storeRead:
		return "use TrawlerCommandArchiveAccessNone or TrawlerCommandArchiveAccessOptional"
	case storeOptional:
		return "use TrawlerCommandArchiveAccessNone"
	case storeWrite:
		return key + " always writes the archive"
	default:
		return "remove TrawlerCommandArchiveAccess"
	}
}

func declaredFlagNames(command TrawlerCommand) []string {
	fs := flag.NewFlagSet(command.TrawlerCommandName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if command.RegisterTrawlerCommandFlags != nil {
		command.RegisterTrawlerCommandFlags(fs)
	}
	var names []string
	fs.VisitAll(func(f *flag.Flag) {
		names = append(names, f.Name)
	})
	sort.Strings(names)
	return names
}

func sharedTrawlerCommandFlagCollisions(key string, names []string) []string {
	owned := runnerOwnedSharedTrawlerCommandFlags(key)
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

func runnerOwnedSharedTrawlerCommandFlags(key string) map[string]struct{} {
	owned := runnerOwnedGlobalFlagNames()
	if key == "search" {
		for name := range runnerOwnedSearchFlagNames() {
			owned[name] = struct{}{}
		}
	}
	if key == "conversations" {
		for name := range runnerOwnedConversationFlagNames() {
			owned[name] = struct{}{}
		}
	}
	if key == "messages" {
		for name := range runnerOwnedTrawlerMessageListFlagNames() {
			owned[name] = struct{}{}
		}
	}
	return owned
}

func parseSharedTrawlerCommandFlags(command TrawlerCommand, args []string, keepDelimiter bool) ([]string, error) {
	if command.RegisterTrawlerCommandFlags == nil {
		return append([]string(nil), args...), nil
	}
	fs := flag.NewFlagSet(command.TrawlerCommandName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	command.RegisterTrawlerCommandFlags(fs)
	flagArgs, rest, err := sharedTrawlerCommandFlagArguments(fs, args, keepDelimiter)
	if err != nil {
		return nil, err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, output.UsageError{Err: err}
	}
	return rest, nil
}

func sharedTrawlerCommandFlagArguments(fs *flag.FlagSet, args []string, keepDelimiter bool) ([]string, []string, error) {
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
			return nil, nil, output.UsageError{Err: fmt.Errorf("%s needs a value.", name)}
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
