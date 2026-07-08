package cli

import (
	"github.com/openclaw/crawlkit"
	imsgcrawl "github.com/openclaw/imsgcrawl"
	photoscrawl "github.com/openclaw/photoscrawl"
	telecrawl "github.com/openclaw/telecrawl"
	wacrawl "github.com/openclaw/wacrawl"
	"github.com/opentrawl/opentrawl/birdcrawl"
	"github.com/opentrawl/opentrawl/calcrawl"
	"github.com/opentrawl/opentrawl/gogcrawl"
)

var crawlerFactories = []func() crawlkit.Crawler{
	func() crawlkit.Crawler { return imsgcrawl.New() },
	func() crawlkit.Crawler { return telecrawl.New() },
	func() crawlkit.Crawler { return wacrawl.New() },
	func() crawlkit.Crawler { return photoscrawl.New() },
	func() crawlkit.Crawler { return gogcrawl.New() },
	func() crawlkit.Crawler { return calcrawl.New() },
	func() crawlkit.Crawler { return birdcrawl.New() },
}

func registeredCrawlers() []crawlkit.Crawler {
	sources := make([]crawlkit.Crawler, 0, len(crawlerFactories))
	for _, factory := range crawlerFactories {
		sources = append(sources, factory())
	}
	return sources
}

func ExecuteCrawlerWire(args []string) int {
	return crawlkit.Run(args, registeredCrawlers())
}
