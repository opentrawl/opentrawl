package main

import (
	"os"

	money "github.com/opentrawl/opentrawl/trawlers/money"
	"github.com/opentrawl/opentrawl/trawlkit/pluginruntime"
)

func main() {
	os.Exit(pluginruntime.ServePluginExecutable(money.New()))
}
