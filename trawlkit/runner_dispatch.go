package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

type targetVerb struct {
	name      string
	tokens    []string
	args      []string
	mutates   bool
	timeout   time.Duration
	spine     *Verb
	bespoke   *Verb
	storeMode storeMode
}

type storeMode int

const (
	storeNone storeMode = iota
	storeOptional
	storeRead
	storeWrite
)

func (r runner) dispatch(ctx context.Context, source Crawler, args []string, globals globalOptions, format output.Format, wireChild bool) executionResult {
	verb, err := resolveVerb(source, args)
	if err != nil {
		return executionResult{err: err}
	}
	if verb.mutates && !wireChild {
		return r.runChild(ctx, source, verb, globals, format)
	}
	return r.runInProcess(ctx, source, verb, globals, format, wireChild)
}

func resolveVerb(source Crawler, args []string) (targetVerb, error) {
	if len(args) == 0 {
		return targetVerb{}, usageError{err: errors.New("verb is required")}
	}
	if verb, ok, err := resolvePrefixedBespokeVerb(source, args); ok || err != nil {
		return verb, err
	}
	name := args[0]
	rest := args[1:]
	if name == "contacts" && len(rest) > 0 && rest[0] == "export" {
		name = "contacts_export"
		rest = rest[1:]
	}
	switch name {
	case "metadata":
		spine, err := supportedVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		decl := spineDeclaration(spine, name)
		return targetVerb{name: name, args: rest, spine: decl, storeMode: spineStoreMode(name, decl)}, nil
	case "status":
		spine, err := supportedVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		decl := spineDeclaration(spine, name)
		return targetVerb{name: name, args: rest, spine: decl, storeMode: spineStoreMode(name, decl)}, nil
	case "sync":
		if _, ok := source.(Syncer); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support sync")}
		}
		spine, err := supportedVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		decl := spineDeclaration(spine, name)
		return targetVerb{name: name, args: rest, mutates: true, spine: decl, storeMode: spineStoreMode(name, decl)}, nil
	case "search":
		if _, ok := source.(Searcher); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support search")}
		}
		spine, err := supportedVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		decl := spineDeclaration(spine, name)
		return targetVerb{name: name, args: rest, spine: decl, storeMode: spineStoreMode(name, decl)}, nil
	case "open":
		if _, ok := source.(Opener); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support open")}
		}
		spine, err := supportedVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		decl := spineDeclaration(spine, name)
		return targetVerb{name: name, args: rest, spine: decl, storeMode: spineStoreMode(name, decl)}, nil
	case "who":
		if _, ok := source.(WhoMatcher); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support who")}
		}
		spine, err := supportedVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		decl := spineDeclaration(spine, name)
		return targetVerb{name: name, args: rest, spine: decl, storeMode: spineStoreMode(name, decl)}, nil
	case "chats":
		if _, ok := source.(ChatLister); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support chats")}
		}
		spine, err := supportedVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		decl := spineDeclaration(spine, name)
		return targetVerb{name: name, args: rest, spine: decl, storeMode: spineStoreMode(name, decl)}, nil
	case "contacts_export":
		if _, ok := source.(ContactExporter); !ok {
			return targetVerb{}, usageError{err: errors.New("source does not support contacts export")}
		}
		spine, err := supportedVerbDeclarations(source)
		if err != nil {
			return targetVerb{}, err
		}
		decl := spineDeclaration(spine, name)
		return targetVerb{name: name, args: rest, spine: decl, storeMode: spineStoreMode(name, decl)}, nil
	}
	for _, verb := range source.Verbs() {
		if matched, verbRest := matchBespokeVerb(verb, args); matched {
			v := verb
			mode, err := storeModeForVerb(verb)
			if err != nil {
				return targetVerb{}, err
			}
			return targetVerb{name: commandKey(verb.Name), tokens: strings.Fields(verb.Name), args: verbRest, mutates: verb.Mutates, timeout: verb.Timeout, bespoke: &v, storeMode: mode}, nil
		}
	}
	return targetVerb{}, usageError{err: fmt.Errorf("unknown verb %q", name)}
}

func resolvePrefixedBespokeVerb(source Crawler, args []string) (targetVerb, bool, error) {
	for _, verb := range source.Verbs() {
		if _, ok := spineVerbKey(verb.Name); ok {
			continue
		}
		if len(strings.Fields(verb.Name)) < 2 {
			continue
		}
		if matched, verbRest := matchBespokeVerb(verb, args); matched {
			v := verb
			mode, err := storeModeForVerb(verb)
			if err != nil {
				return targetVerb{}, true, err
			}
			return targetVerb{name: commandKey(verb.Name), tokens: strings.Fields(verb.Name), args: verbRest, mutates: verb.Mutates, timeout: verb.Timeout, bespoke: &v, storeMode: mode}, true, nil
		}
	}
	return targetVerb{}, false, nil
}

func (verb targetVerb) childArgs() []string {
	if len(verb.tokens) > 0 {
		return append([]string(nil), verb.tokens...)
	}
	if verb.name == "contacts_export" {
		return []string{"contacts", "export"}
	}
	return []string{verb.name}
}

func matchBespokeVerb(verb Verb, args []string) (bool, []string) {
	parts := strings.Fields(verb.Name)
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
