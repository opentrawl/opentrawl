package trawlkit

import (
	"context"
	"errors"
	"strings"
	"time"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type targetTrawlerCommand struct {
	name                     string
	tokens                   []string
	args                     []string
	invocationArguments      []string
	mutates                  bool
	timeout                  time.Duration
	sharedOperation          federation.SharedTrawlerOperation
	shared                   *TrawlerCommand
	bespoke                  *TrawlerCommand
	storeMode                storeMode
	sharedOperationExecution sharedTrawlerOperationExecution
	flagsParsed              bool
	writesNoFiles            bool
}

func (command targetTrawlerCommand) commandName() string {
	if command.sharedOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
		return sharedTrawlerOperationCommandName(command.sharedOperation)
	}
	return command.name
}

type storeMode int

const (
	storeNone storeMode = iota
	storeOptional
	storeRead
	storeWrite
	storeReadWithoutWritingFiles
)

func (r runner) dispatch(ctx context.Context, source Trawler, args []string, globals globalOptions, wireChild bool) executionResult {
	command, err := resolveTrawlerCommand(source, args)
	if err != nil {
		return executionResult{err: err}
	}
	command, err = parseTargetTrawlerCommandFlags(command)
	if err != nil {
		return executionResult{err: err}
	}
	if command.mutates && !wireChild {
		return r.runChild(ctx, source, command, globals)
	}
	return r.runInProcess(ctx, source, command, globals, wireChild)
}

func parseTargetTrawlerCommandFlags(targetCommand targetTrawlerCommand) (targetTrawlerCommand, error) {
	if targetCommand.bespoke == nil || targetCommand.flagsParsed {
		return targetCommand, nil
	}
	positionalArguments, err := parseBespokeFlags(*targetCommand.bespoke, targetCommand.args)
	if err != nil {
		return targetCommand, err
	}
	if err := validateBespokeArgs(*targetCommand.bespoke, positionalArguments); err != nil {
		return targetCommand, err
	}
	targetCommand.args = positionalArguments
	targetCommand.flagsParsed = true
	if resolveAccess := targetCommand.bespoke.TrawlerCommandArchiveAccessWithoutWrites; resolveAccess != nil {
		archiveAccess, writesNoFiles := resolveAccess(positionalArguments)
		if writesNoFiles {
			readOnlyDeclaration := *targetCommand.bespoke
			readOnlyDeclaration.TrawlerCommandArchiveAccess = archiveAccess
			readOnlyDeclaration.TrawlerCommandChangesArchive = false
			archiveMode, err := storeModeForTrawlerCommand(readOnlyDeclaration)
			if err != nil {
				return targetCommand, err
			}
			targetCommand.storeMode = archiveMode
			if archiveMode == storeRead {
				targetCommand.storeMode = storeReadWithoutWritingFiles
			}
			targetCommand.mutates = false
			targetCommand.writesNoFiles = true
		}
	}
	return targetCommand, nil
}

func DeclaredTrawlerCommandWritesNoFiles(trawler Trawler, arguments []string) (bool, error) {
	targetCommand, err := resolveTrawlerCommand(trawler, arguments)
	if err != nil {
		return false, err
	}
	targetCommand, err = parseTargetTrawlerCommandFlags(targetCommand)
	return targetCommand.writesNoFiles, err
}

func resolveTrawlerCommand(trawler Trawler, args []string) (targetTrawlerCommand, error) {
	if len(args) == 0 {
		return targetTrawlerCommand{}, usageError{err: errors.New("command is required")}
	}
	if command, ok, err := resolvePrefixedBespokeTrawlerCommand(trawler, args); ok || err != nil {
		return command, err
	}
	name := args[0]
	rest := args[1:]
	if sharedOperation, isSharedOperation := sharedTrawlerOperationForCommandName(name); isSharedOperation {
		sharedCommands, err := validatedTrawlerCommandDeclarations(trawler)
		if err != nil {
			return targetTrawlerCommand{}, err
		}
		declaration := sharedTrawlerCommandDeclaration(sharedCommands, sharedOperation)
		if declaration == nil {
			return targetTrawlerCommand{}, usageError{err: render.UnknownCommand(name, nil)}
		}
		return targetTrawlerCommand{
			args:            rest,
			mutates:         sharedOperation == federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
			sharedOperation: sharedOperation,
			shared:          declaration,
			storeMode:       sharedTrawlerCommandArchiveAccessMode(sharedOperation, declaration),
		}, nil
	}
	for _, command := range trawler.TrawlerCommands() {
		if command.SharedTrawlerOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
			continue
		}
		if matched, remainingCommandArguments := matchBespokeTrawlerCommand(command, args); matched {
			v := command
			mode, err := storeModeForTrawlerCommand(command)
			if err != nil {
				return targetTrawlerCommand{}, err
			}
			return targetTrawlerCommand{name: commandKey(command.TrawlerCommandName), tokens: strings.Fields(command.TrawlerCommandName), args: remainingCommandArguments, invocationArguments: append([]string(nil), remainingCommandArguments...), mutates: command.TrawlerCommandChangesArchive, timeout: command.TrawlerCommandMaximumExecutionTime, bespoke: &v, storeMode: mode}, nil
		}
	}
	return targetTrawlerCommand{}, usageError{err: render.UnknownCommand(name, nil)}
}

func resolvePrefixedBespokeTrawlerCommand(trawler Trawler, args []string) (targetTrawlerCommand, bool, error) {
	for _, command := range trawler.TrawlerCommands() {
		if command.SharedTrawlerOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
			continue
		}
		if len(strings.Fields(command.TrawlerCommandName)) < 2 {
			continue
		}
		if matched, remainingCommandArguments := matchBespokeTrawlerCommand(command, args); matched {
			v := command
			mode, err := storeModeForTrawlerCommand(command)
			if err != nil {
				return targetTrawlerCommand{}, true, err
			}
			return targetTrawlerCommand{name: commandKey(command.TrawlerCommandName), tokens: strings.Fields(command.TrawlerCommandName), args: remainingCommandArguments, invocationArguments: append([]string(nil), remainingCommandArguments...), mutates: command.TrawlerCommandChangesArchive, timeout: command.TrawlerCommandMaximumExecutionTime, bespoke: &v, storeMode: mode}, true, nil
		}
	}
	return targetTrawlerCommand{}, false, nil
}

func (command targetTrawlerCommand) childArgs() []string {
	if command.sharedOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
		return []string{sharedTrawlerOperationCommandName(command.sharedOperation)}
	}
	if len(command.tokens) > 0 {
		return append([]string(nil), command.tokens...)
	}
	return []string{command.name}
}

func matchBespokeTrawlerCommand(command TrawlerCommand, args []string) (bool, []string) {
	parts := strings.Fields(command.TrawlerCommandName)
	if len(parts) == 0 || len(args) < len(parts) {
		return false, nil
	}
	for i, part := range parts {
		if args[i] != part {
			return false, nil
		}
	}
	return true, append([]string(nil), args[len(parts):]...)
}
