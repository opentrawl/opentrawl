package trawlkit

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

var sharedTrawlerOperationCommandNames = map[federationv1.SharedTrawlerOperation]string{
	federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_METADATA:      "metadata",
	federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS:        "status",
	federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC:          "update",
	federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH:        "search",
	federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN:          "open",
	federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO:           "who",
	federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS: "conversations",
	federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES:      "messages",
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

func sharedTrawlerCommandDeclarations(trawler Trawler) (map[federationv1.SharedTrawlerOperation]TrawlerCommand, error) {
	declarations := map[federationv1.SharedTrawlerOperation]TrawlerCommand{}
	for _, command := range trawler.TrawlerCommands() {
		operation := command.SharedTrawlerOperation
		if operation == federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
			continue
		}
		if err := validateSharedTrawlerCommand(
			operation,
			command,
			declarations,
			unsupportedSharedTrawlerCommandError(trawler, operation),
		); err != nil {
			return nil, err
		}
		declarations[operation] = command
	}
	return declarations, nil
}

func supportedSharedTrawlerCommandDeclarations(trawler Trawler) (map[federationv1.SharedTrawlerOperation]TrawlerCommand, error) {
	declarations := map[federationv1.SharedTrawlerOperation]TrawlerCommand{}
	for _, command := range trawler.TrawlerCommands() {
		operation := command.SharedTrawlerOperation
		if operation == federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
			continue
		}
		if unsupportedSharedTrawlerCommandInterface(trawler, operation) != "" {
			continue
		}
		if err := validateSharedTrawlerCommand(operation, command, declarations, nil); err != nil {
			return nil, err
		}
		declarations[operation] = command
	}
	return declarations, nil
}

func sharedTrawlerCommandDeclaration(
	sharedCommands map[federationv1.SharedTrawlerOperation]TrawlerCommand,
	operation federationv1.SharedTrawlerOperation,
) *TrawlerCommand {
	command, ok := sharedCommands[operation]
	if !ok {
		return nil
	}
	return &command
}

func validateSharedTrawlerCommand(
	operation federationv1.SharedTrawlerOperation,
	command TrawlerCommand,
	declarations map[federationv1.SharedTrawlerOperation]TrawlerCommand,
	supportError error,
) error {
	commandName := sharedTrawlerOperationCommandName(operation)
	if commandName == "" {
		return invalidSharedTrawlerOperationError(operation)
	}
	if fields := invalidSharedTrawlerCommandFields(command); len(fields) > 0 {
		return invalidSharedTrawlerCommandFieldsError(commandName, fields)
	}
	if supportError != nil {
		return supportError
	}
	if _, ok := declarations[operation]; ok {
		return duplicateSharedTrawlerCommandError(commandName)
	}
	if collisions := sharedTrawlerCommandFlagCollisions(operation, declaredFlagNames(command)); len(collisions) > 0 {
		return sharedTrawlerCommandFlagCollisionError(commandName, collisions)
	}
	if err := validateSharedTrawlerCommandArchiveAccess(operation, command.TrawlerCommandArchiveAccess); err != nil {
		return err
	}
	return nil
}

func unsupportedSharedTrawlerCommandError(trawler Trawler, operation federationv1.SharedTrawlerOperation) error {
	if interfaceName := unsupportedSharedTrawlerCommandInterface(trawler, operation); interfaceName != "" {
		return unsupportedSharedTrawlerCommandInterfaceError(sharedTrawlerOperationCommandName(operation), interfaceName)
	}
	return nil
}

func invalidSharedTrawlerCommandFieldsError(key string, fields []string) sharedTrawlerCommandError {
	return sharedTrawlerCommandError{
		message: fmt.Sprintf("invalid %s TrawlerCommand declaration: shared command declarations may only set TrawlerCommandName, RegisterTrawlerCommandFlags, TrawlerCommandArchiveAccess, and TrawlerCommandHelpListing", key),
	}
}

func invalidSharedTrawlerCommandArchiveAccessError(
	operation federationv1.SharedTrawlerOperation,
	declared TrawlerCommandArchiveAccess,
) sharedTrawlerCommandError {
	commandName := sharedTrawlerOperationCommandName(operation)
	return sharedTrawlerCommandError{
		message: fmt.Sprintf(
			"invalid %s TrawlerCommand declaration: %s is not valid; %s",
			commandName,
			trawlerCommandArchiveAccessName(declared),
			sharedTrawlerCommandArchiveAccessAllowance(operation),
		),
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

func invalidSharedTrawlerOperationError(operation federationv1.SharedTrawlerOperation) error {
	return sharedTrawlerCommandError{
		message: fmt.Sprintf("invalid shared trawler operation %s", operation.String()),
	}
}

func sharedTrawlerOperationCommandName(operation federationv1.SharedTrawlerOperation) string {
	return sharedTrawlerOperationCommandNames[operation]
}

func SharedTrawlerOperationCommandName(operation federationv1.SharedTrawlerOperation) string {
	return sharedTrawlerOperationCommandName(operation)
}

func sharedTrawlerOperationForCommandName(name string) (federationv1.SharedTrawlerOperation, bool) {
	wantedCommandName := commandKey(name)
	for operation, commandName := range sharedTrawlerOperationCommandNames {
		if wantedCommandName == commandName {
			return operation, true
		}
	}
	return federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED, false
}

func invalidSharedTrawlerCommandFields(command TrawlerCommand) []string {
	if strings.TrimSpace(command.TrawlerCommandName) != "" {
		return []string{"TrawlerCommandName"}
	}
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

func unsupportedSharedTrawlerCommandInterface(trawler Trawler, operation federationv1.SharedTrawlerOperation) string {
	switch operation {
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_METADATA,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS:
		return ""
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC:
		if _, ok := trawler.(Syncer); !ok {
			return "Syncer"
		}
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH:
		if _, ok := trawler.(Searcher); !ok {
			return "Searcher"
		}
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN:
		if _, ok := trawler.(RecordOpener); !ok {
			return "RecordOpener"
		}
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO:
		if _, ok := trawler.(WhoMatcher); !ok {
			return "WhoMatcher"
		}
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS:
		if _, ok := trawler.(ConversationLister); !ok {
			return "ConversationLister"
		}
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES:
		if _, ok := trawler.(TrawlerMessageLister); !ok {
			return "TrawlerMessageLister"
		}
	}
	return ""
}

func validateSharedTrawlerCommandArchiveAccess(operation federationv1.SharedTrawlerOperation, declared TrawlerCommandArchiveAccess) error {
	if declared == TrawlerCommandArchiveAccessDefault {
		return nil
	}
	switch defaultSharedTrawlerCommandArchiveAccessMode(operation) {
	case storeRead:
		if declared == TrawlerCommandArchiveAccessNone || declared == TrawlerCommandArchiveAccessOptional {
			return nil
		}
	case storeOptional:
		if declared == TrawlerCommandArchiveAccessNone {
			return nil
		}
	}
	return invalidSharedTrawlerCommandArchiveAccessError(operation, declared)
}

func sharedTrawlerCommandArchiveAccessMode(operation federationv1.SharedTrawlerOperation, command *TrawlerCommand) storeMode {
	if command == nil || command.TrawlerCommandArchiveAccess == TrawlerCommandArchiveAccessDefault {
		return defaultSharedTrawlerCommandArchiveAccessMode(operation)
	}
	switch command.TrawlerCommandArchiveAccess {
	case TrawlerCommandArchiveAccessNone:
		return storeNone
	case TrawlerCommandArchiveAccessOptional:
		return storeOptional
	default:
		return defaultSharedTrawlerCommandArchiveAccessMode(operation)
	}
}

func defaultSharedTrawlerCommandArchiveAccessMode(operation federationv1.SharedTrawlerOperation) storeMode {
	switch operation {
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_METADATA:
		return storeNone
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS:
		return storeOptional
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC:
		return storeWrite
	case federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES:
		return storeRead
	default:
		return storeRead
	}
}

func sharedTrawlerCommandArchiveAccessAllowance(operation federationv1.SharedTrawlerOperation) string {
	commandName := sharedTrawlerOperationCommandName(operation)
	switch defaultSharedTrawlerCommandArchiveAccessMode(operation) {
	case storeRead:
		return "use TrawlerCommandArchiveAccessNone or TrawlerCommandArchiveAccessOptional"
	case storeOptional:
		return "use TrawlerCommandArchiveAccessNone"
	case storeWrite:
		return commandName + " always writes the archive"
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

func sharedTrawlerCommandFlagCollisions(operation federationv1.SharedTrawlerOperation, names []string) []string {
	owned := runnerOwnedSharedTrawlerCommandFlags(operation)
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

func runnerOwnedSharedTrawlerCommandFlags(operation federationv1.SharedTrawlerOperation) map[string]struct{} {
	owned := runnerOwnedGlobalFlagNames()
	if operation == federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH {
		for name := range runnerOwnedSearchFlagNames() {
			owned[name] = struct{}{}
		}
	}
	if operation == federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS {
		for name := range runnerOwnedConversationFlagNames() {
			owned[name] = struct{}{}
		}
	}
	if operation == federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES {
		for name := range runnerOwnedTrawlerMessageListFlagNames() {
			owned[name] = struct{}{}
		}
	}
	return owned
}

func parseSharedTrawlerCommandFlags(
	operation federationv1.SharedTrawlerOperation,
	command *TrawlerCommand,
	args []string,
	keepDelimiter bool,
) ([]string, error) {
	if command == nil || command.RegisterTrawlerCommandFlags == nil {
		remainingArguments := append([]string(nil), args...)
		if err := validateArchiveUpdateHasNoUnusedArguments(operation, remainingArguments); err != nil {
			return nil, err
		}
		return remainingArguments, nil
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
	if err := validateArchiveUpdateHasNoUnusedArguments(operation, rest); err != nil {
		return nil, err
	}
	return rest, nil
}

func validateArchiveUpdateHasNoUnusedArguments(
	operation federationv1.SharedTrawlerOperation,
	remainingArguments []string,
) error {
	if operation != federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC ||
		len(remainingArguments) == 0 {
		return nil
	}
	if strings.HasPrefix(remainingArguments[0], "-") {
		optionName, _, _ := strings.Cut(remainingArguments[0], "=")
		return output.UsageError{Err: output.HumanFacingErrorMessage("Unknown option " + optionName + ".")}
	}
	return output.UsageError{Err: output.HumanFacingErrorMessage("Update does not accept positional arguments.")}
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
