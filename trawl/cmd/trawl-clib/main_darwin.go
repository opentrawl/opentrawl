package main

import "C"

import (
	"unsafe"

	"github.com/opentrawl/opentrawl/trawl/internal/cli"
)

//export OpenTrawlRunCommandLine
func OpenTrawlRunCommandLine(argumentCount C.int, argumentVector **C.char) C.int {
	arguments := make([]string, int(argumentCount))
	for index, argument := range unsafe.Slice(argumentVector, len(arguments)) {
		arguments[index] = C.GoString(argument)
	}
	return C.int(cli.RunCommandLine(arguments))
}

func main() {}
