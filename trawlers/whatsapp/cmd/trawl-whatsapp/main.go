package main

import (
	"os"

	whatsapp "github.com/opentrawl/opentrawl/trawlers/whatsapp"
	"github.com/opentrawl/opentrawl/trawlkit/pluginruntime"
)

func main() {
	os.Exit(pluginruntime.ServePluginExecutable(whatsapp.New()))
}
