package cli

import (
	"github.com/opentrawl/opentrawl/birdcrawl"
	"github.com/opentrawl/opentrawl/calcrawl"
	"github.com/opentrawl/opentrawl/gogcrawl"
	clawdex "github.com/opentrawl/opentrawl/trawlers/contacts"
	imsgcrawl "github.com/opentrawl/opentrawl/trawlers/imessage"
	notes "github.com/opentrawl/opentrawl/trawlers/notes"
	photoscrawl "github.com/opentrawl/opentrawl/trawlers/photos"
	telecrawl "github.com/opentrawl/opentrawl/trawlers/telegram"
	wacrawl "github.com/opentrawl/opentrawl/trawlers/whatsapp"
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
