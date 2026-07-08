package crawlkit

import (
	"errors"
	"fmt"
	"strings"
)

func selectSource(args []string, sources []Crawler) (Crawler, []string, error) {
	if len(sources) == 0 {
		return nil, nil, usageError{err: errors.New("no crawlers are registered")}
	}
	if len(sources) == 1 {
		source := sources[0]
		if len(args) > 0 && matchesSource(source.Info(), args[0]) {
			return source, args[1:], nil
		}
		return source, args, nil
	}
	if len(args) == 0 {
		return nil, nil, usageError{err: errors.New("source is required")}
	}
	var matches []Crawler
	for _, source := range sources {
		if matchesSource(source.Info(), args[0]) {
			matches = append(matches, source)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil, usageError{err: fmt.Errorf("unknown source %q", args[0])}
	case 1:
		return matches[0], args[1:], nil
	default:
		return nil, nil, usageError{err: fmt.Errorf("ambiguous source %q matches %s", args[0], sourceIDs(matches))}
	}
}

func matchesSource(info Info, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if token == info.ID || token == info.Surface {
		return true
	}
	for _, alias := range info.Aliases {
		if token == strings.TrimSpace(alias) {
			return true
		}
	}
	return false
}

func sourceIDs(sources []Crawler) string {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, firstText(source.Info().ID, source.Info().Surface))
	}
	return strings.Join(ids, ", ")
}
