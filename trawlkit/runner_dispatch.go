package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type targetTrawlerCommand struct {
	name                string
	tokens              []string
	args                []string
	invocationArguments []string
	mutates             bool
	timeout             time.Duration
	shared              *TrawlerCommand
	bespoke             *TrawlerCommand
	storeMode           storeMode
	typed               typedTrawlerOperation
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

func resolveTrawlerCommand(source Trawler, args []string) (targetTrawlerCommand, error) {
	if len(args) == 0 {
		return targetTrawlerCommand{}, usageError{err: errors.New("command is required")}
	}
	if command, ok, err := resolvePrefixedBespokeTrawlerCommand(source, args); ok || err != nil {
		return command, err
	}
	name := args[0]
	rest := args[1:]
	switch name {
	case "metadata":
		sharedCommands, err := supportedTrawlerCommandDeclarations(source)
		if err != nil {
			return targetTrawlerCommand{}, err
		}
		declaration := sharedTrawlerCommandDeclaration(sharedCommands, name)
		return targetTrawlerCommand{name: name, args: rest, shared: declaration, storeMode: sharedTrawlerCommandArchiveAccessMode(name, declaration)}, nil
	case "status":
		sharedCommands, err := supportedTrawlerCommandDeclarations(source)
		if err != nil {
			return targetTrawlerCommand{}, err
		}
		declaration := sharedTrawlerCommandDeclaration(sharedCommands, name)
		return targetTrawlerCommand{name: name, args: rest, shared: declaration, storeMode: sharedTrawlerCommandArchiveAccessMode(name, declaration)}, nil
	case "sync":
		if _, ok := source.(Syncer); !ok {
			return targetTrawlerCommand{}, usageError{err: errors.New("trawler does not support sync")}
		}
		sharedCommands, err := supportedTrawlerCommandDeclarations(source)
		if err != nil {
			return targetTrawlerCommand{}, err
		}
		declaration := sharedTrawlerCommandDeclaration(sharedCommands, name)
		return targetTrawlerCommand{name: name, args: rest, mutates: true, shared: declaration, storeMode: sharedTrawlerCommandArchiveAccessMode(name, declaration)}, nil
	case "search":
		if _, ok := source.(Searcher); !ok {
			return targetTrawlerCommand{}, usageError{err: errors.New("trawler does not support search")}
		}
		sharedCommands, err := supportedTrawlerCommandDeclarations(source)
		if err != nil {
			return targetTrawlerCommand{}, err
		}
		declaration := sharedTrawlerCommandDeclaration(sharedCommands, name)
		return targetTrawlerCommand{name: name, args: rest, shared: declaration, storeMode: sharedTrawlerCommandArchiveAccessMode(name, declaration)}, nil
	case "open":
		if _, ok := source.(RecordOpener); !ok {
			return targetTrawlerCommand{}, usageError{err: errors.New("trawler does not support open")}
		}
		sharedCommands, err := supportedTrawlerCommandDeclarations(source)
		if err != nil {
			return targetTrawlerCommand{}, err
		}
		declaration := sharedTrawlerCommandDeclaration(sharedCommands, name)
		return targetTrawlerCommand{name: name, args: rest, shared: declaration, storeMode: sharedTrawlerCommandArchiveAccessMode(name, declaration)}, nil
	case "who":
		if _, ok := source.(WhoMatcher); !ok {
			return targetTrawlerCommand{}, usageError{err: errors.New("trawler does not support who")}
		}
		sharedCommands, err := supportedTrawlerCommandDeclarations(source)
		if err != nil {
			return targetTrawlerCommand{}, err
		}
		declaration := sharedTrawlerCommandDeclaration(sharedCommands, name)
		return targetTrawlerCommand{name: name, args: rest, shared: declaration, storeMode: sharedTrawlerCommandArchiveAccessMode(name, declaration)}, nil
	case "conversations":
		if _, ok := source.(ConversationLister); !ok {
			return targetTrawlerCommand{}, usageError{err: errors.New("trawler does not support conversations")}
		}
		sharedCommands, err := supportedTrawlerCommandDeclarations(source)
		if err != nil {
			return targetTrawlerCommand{}, err
		}
		declaration := sharedTrawlerCommandDeclaration(sharedCommands, name)
		return targetTrawlerCommand{name: name, args: rest, shared: declaration, storeMode: sharedTrawlerCommandArchiveAccessMode(name, declaration)}, nil
	case "messages":
		if _, ok := source.(TrawlerMessageLister); !ok {
			return targetTrawlerCommand{}, usageError{err: errors.New("trawler does not support messages")}
		}
		sharedCommands, err := supportedTrawlerCommandDeclarations(source)
		if err != nil {
			return targetTrawlerCommand{}, err
		}
		declaration := sharedTrawlerCommandDeclaration(sharedCommands, name)
		return targetTrawlerCommand{name: name, args: rest, shared: declaration, storeMode: sharedTrawlerCommandArchiveAccessMode(name, declaration)}, nil
	}
	for _, command := range source.TrawlerCommands() {
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

func resolvePrefixedBespokeTrawlerCommand(source Trawler, args []string) (targetTrawlerCommand, bool, error) {
	for _, command := range source.TrawlerCommands() {
		if _, ok := sharedTrawlerCommandName(command.TrawlerCommandName); ok {
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
