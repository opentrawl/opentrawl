package main

import (
	"os"

	beeper "github.com/opentrawl/opentrawl/plugins/trawl-beeper"
	"github.com/opentrawl/opentrawl/trawlkit/pluginruntime"
)

func main() {
	os.Exit(pluginruntime.ServePluginExecutable(beeper.NewBeeperTrawler()))
}
