package main

import (
	"os"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/photoscrawl"
)

func main() {
	os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{photoscrawl.New()}))
}
