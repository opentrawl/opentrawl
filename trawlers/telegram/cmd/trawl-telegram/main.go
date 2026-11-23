package main

import (
	"os"

	telegram "github.com/opentrawl/opentrawl/trawlers/telegram"
	"github.com/opentrawl/opentrawl/trawlkit/pluginruntime"
)

func main() {
	os.Exit(pluginruntime.ServePluginExecutable(telegram.New()))
}
