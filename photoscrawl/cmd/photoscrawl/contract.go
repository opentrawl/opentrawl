package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/output"
)

type commandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Remedy  string `json:"remedy"`
}

func (e commandError) Error() string {
	return e.Message
}

func writeError(w io.Writer, err error) error {
	contractErr := normaliseError(err)
	return json.NewEncoder(w).Encode(map[string]commandError{"error": contractErr})
}

func humanError(err error) string {
	contractErr := normaliseError(err)
	if contractErr.Remedy == "" {
		return contractErr.Message
	}
	return contractErr.Message + ". Remedy: " + contractErr.Remedy
}

func normaliseError(err error) commandError {
	var contractErr commandError
	if errors.As(err, &contractErr) {
		return contractErr
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "command failed"
	}
	switch {
	case output.IsUsage(err):
		return commandError{Code: "usage", Message: message, Remedy: "use photoscrawl <verb> [arguments] [flags]"}
	case strings.Contains(message, "photokit export already running"):
		return commandError{Code: "export_already_running", Message: message, Remedy: "wait for the other export run to finish, then rerun the command"}
	case strings.Contains(message, "not found"):
		return commandError{Code: "not_found", Message: message, Remedy: "use a ref returned by photoscrawl search"}
	default:
		return commandError{Code: "command_failed", Message: message, Remedy: "fix the reported problem and rerun the command"}
	}
}

func wantsJSON(args []string) bool {
	for i, arg := range args {
		if arg == "--json" || arg == "--format=json" {
			return true
		}
		if arg == "--format" && i+1 < len(args) && args[i+1] == "json" {
			return true
		}
	}
	return false
}

type searchCommand struct {
	DBPath string
	Query  string
	Limit  int
	After  string
	Before string
	Format output.Format
}

func parseSearchCommand(args []string) (searchCommand, error) {
	parsed := searchCommand{Limit: 20}
	var jsonFlag bool
	var formatFlag string
	query := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			name, value, hasValue := splitFlag(arg)
			switch name {
			case "--json":
				if hasValue {
					return parsed, commandError{Code: "usage", Message: "--json does not take a value", Remedy: "pass --json as a flag"}
				}
				jsonFlag = true
			case "--limit":
				var err error
				value, i, err = flagValue(args, i, value, hasValue, "--limit")
				if err != nil {
					return parsed, err
				}
				limit, err := strconv.Atoi(value)
				if err != nil {
					return parsed, commandError{Code: "usage", Message: "--limit must be an integer", Remedy: "use --limit with a number from 1 to 200"}
				}
				parsed.Limit = limit
			case "--after":
				var err error
				parsed.After, i, err = flagValue(args, i, value, hasValue, "--after")
				if err != nil {
					return parsed, err
				}
			case "--before":
				var err error
				parsed.Before, i, err = flagValue(args, i, value, hasValue, "--before")
				if err != nil {
					return parsed, err
				}
			case "--db":
				var err error
				parsed.DBPath, i, err = flagValue(args, i, value, hasValue, "--db")
				if err != nil {
					return parsed, err
				}
			case "--format":
				var err error
				formatFlag, i, err = flagValue(args, i, value, hasValue, "--format")
				if err != nil {
					return parsed, err
				}
			default:
				return parsed, commandError{Code: "usage", Message: fmt.Sprintf("unknown search flag %s", name), Remedy: "use search <query> --limit N --json"}
			}
			continue
		}
		query = append(query, arg)
	}
	parsed.Query = strings.TrimSpace(strings.Join(query, " "))
	if parsed.Query == "" {
		return parsed, commandError{Code: "missing_query", Message: "search query is required", Remedy: "use search <query> [flags]"}
	}
	format, err := output.Resolve(formatFlag, jsonFlag)
	if err != nil {
		return parsed, err
	}
	parsed.Format = format
	return parsed, nil
}

type refCommand struct {
	DBPath string
	Ref    string
	Limit  int
	Format output.Format
}

func parseRefCommand(verb string, args []string, withLimit bool) (refCommand, error) {
	parsed := refCommand{Limit: 20}
	var jsonFlag bool
	var formatFlag string
	flagsStarted := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			flagsStarted = true
			name, value, hasValue := splitFlag(arg)
			switch name {
			case "--json":
				if hasValue {
					return parsed, commandError{Code: "usage", Message: "--json does not take a value", Remedy: "pass --json after the ref"}
				}
				jsonFlag = true
			case "--limit":
				if !withLimit {
					return parsed, commandError{Code: "usage", Message: fmt.Sprintf("unknown %s flag --limit", verb), Remedy: fmt.Sprintf("use %s <ref> --json", verb)}
				}
				var err error
				value, i, err = flagValue(args, i, value, hasValue, "--limit")
				if err != nil {
					return parsed, err
				}
				limit, err := strconv.Atoi(value)
				if err != nil {
					return parsed, commandError{Code: "usage", Message: "--limit must be an integer", Remedy: "use --limit with a number from 1 to 100"}
				}
				parsed.Limit = limit
			case "--db":
				var err error
				parsed.DBPath, i, err = flagValue(args, i, value, hasValue, "--db")
				if err != nil {
					return parsed, err
				}
			case "--format":
				var err error
				formatFlag, i, err = flagValue(args, i, value, hasValue, "--format")
				if err != nil {
					return parsed, err
				}
			default:
				return parsed, commandError{Code: "usage", Message: fmt.Sprintf("unknown %s flag %s", verb, name), Remedy: fmt.Sprintf("use %s <ref> --json", verb)}
			}
			continue
		}
		if flagsStarted {
			return parsed, commandError{Code: "usage", Message: verb + " ref must come before flags", Remedy: fmt.Sprintf("use %s <ref> [flags]", verb)}
		}
		if parsed.Ref != "" {
			return parsed, commandError{Code: "usage", Message: verb + " takes one ref", Remedy: "use a single ref returned by photoscrawl search"}
		}
		parsed.Ref = arg
	}
	if strings.TrimSpace(parsed.Ref) == "" {
		return parsed, commandError{Code: "missing_ref", Message: verb + " ref is required", Remedy: "use a ref returned by photoscrawl search"}
	}
	format, err := output.Resolve(formatFlag, jsonFlag)
	if err != nil {
		return parsed, err
	}
	parsed.Format = format
	return parsed, nil
}

func splitFlag(arg string) (string, string, bool) {
	name, value, ok := strings.Cut(arg, "=")
	return name, value, ok
}

func flagValue(args []string, index int, inline string, hasInline bool, name string) (string, int, error) {
	if hasInline {
		if inline == "" {
			return "", index, commandError{Code: "usage", Message: name + " needs a value", Remedy: "pass a value after " + name}
		}
		return inline, index, nil
	}
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
		return "", index, commandError{Code: "usage", Message: name + " needs a value", Remedy: "pass a value after " + name}
	}
	return args[index+1], index + 1, nil
}
