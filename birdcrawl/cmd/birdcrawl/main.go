package main

import (
	"os"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/birdcrawl"
)

func main() {
	os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{birdcrawl.New()}))
}
