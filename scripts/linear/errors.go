package main

import (
	"errors"
)

type usageError struct {
	message string
}

func (e usageError) Error() string {
	return e.message
}

type helpShown struct{}

func (h helpShown) Error() string {
	return "help shown"
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var help helpShown
	if errors.As(err, &help) {
		return 0
	}
	var usage usageError
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

func shouldPrintError(err error) bool {
	if err == nil {
		return false
	}
	var help helpShown
	return !errors.As(err, &help)
}
