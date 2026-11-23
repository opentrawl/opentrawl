package main

import (
	"os"

	notes "github.com/opentrawl/opentrawl/trawlers/notes"
	"github.com/opentrawl/opentrawl/trawlkit/pluginruntime"
)

func main() {
	os.Exit(pluginruntime.ServePluginExecutable(notes.New()))
}
