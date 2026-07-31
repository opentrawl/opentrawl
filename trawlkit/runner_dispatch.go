package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
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
)

func (r runner) dispatch(ctx context.Context, source Trawler, args []string, globals globalOptions, wireChild bool) executionResult {
	command, err := resolveTrawlerCommand(source, args)
	if err != nil {
		return executionResult{err: err}
	}
	if command.mutates && !wireChild {
		return r.runChild(ctx, source, command, globals)
	}
	return r.runInProcess(ctx, source, command, globals, wireChild)
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
			return targetTrawlerCommand{}, usageError{
				err: fmt.Errorf("trawler does not declare %s", sharedTrawlerOperationCommandName(sharedOperation)),
			}
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
	return targetTrawlerCommand{}, usageError{err: fmt.Errorf("unknown command %q", name)}
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
