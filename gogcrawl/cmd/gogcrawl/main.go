package main

import (
	"os"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/gogcrawl"
)

func main() {
	os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{gogcrawl.New()}))
}
