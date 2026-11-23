package main

import (
	"os"

	"github.com/opentrawl/opentrawl/trawlkit/pluginruntime"
	"github.com/opentrawl/opentrawl/twitter"
)

func main() {
	os.Exit(pluginruntime.ServePluginExecutable(twitter.New()))
}
