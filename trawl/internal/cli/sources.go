package cli

import (
	clawdex "github.com/openclaw/clawdex"
	imsgcrawl "github.com/openclaw/imsgcrawl"
	photoscrawl "github.com/openclaw/photoscrawl"
	telecrawl "github.com/openclaw/telecrawl"
	wacrawl "github.com/openclaw/wacrawl"
	"github.com/opentrawl/opentrawl/birdcrawl"
	"github.com/opentrawl/opentrawl/calcrawl"
	"github.com/opentrawl/opentrawl/gogcrawl"
	notes "github.com/opentrawl/opentrawl/trawlers/notes"
	"github.com/opentrawl/opentrawl/trawlkit"
)

var crawlerFactories = []func() trawlkit.Crawler{
	func() trawlkit.Crawler { return imsgcrawl.New() },
	func() trawlkit.Crawler { return telecrawl.New() },
	func() trawlkit.Crawler { return wacrawl.New() },
	func() trawlkit.Crawler { return photoscrawl.New() },
	func() trawlkit.Crawler { return gogcrawl.New() },
	func() trawlkit.Crawler { return calcrawl.New() },
	func() trawlkit.Crawler { return birdcrawl.New() },
	func() trawlkit.Crawler { return clawdex.New() },
	func() trawlkit.Crawler { return notes.New() },
}

func registeredCrawlers() []trawlkit.Crawler {
	sources := make([]trawlkit.Crawler, 0, len(crawlerFactories))
	for _, factory := range crawlerFactories {
		sources = append(sources, factory())
	}
	return sources
}

func ExecuteCrawlerWire(args []string) int {
	return trawlkit.Run(args, registeredCrawlers())
}
