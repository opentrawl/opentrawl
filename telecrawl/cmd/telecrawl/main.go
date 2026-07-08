package main

import (
	"os"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/telecrawl"
)

func main() {
	os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{telecrawl.New()}))
}
