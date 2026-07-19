package cli

import (
	"github.com/opentrawl/opentrawl/calendar"
	"github.com/opentrawl/opentrawl/gmail"
	contacts "github.com/opentrawl/opentrawl/trawlers/contacts"
	imessage "github.com/opentrawl/opentrawl/trawlers/imessage"
	notes "github.com/opentrawl/opentrawl/trawlers/notes"
	photos "github.com/opentrawl/opentrawl/trawlers/photos"
	telegram "github.com/opentrawl/opentrawl/trawlers/telegram"
	whatsapp "github.com/opentrawl/opentrawl/trawlers/whatsapp"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/twitter"
)

// crawlerFactories is the single source ordering authority: the front
// door, the --help sources block and the status table all iterate this
// slice, so its order is the order a person sees. Messaging first, then
// mail, calendar, people, photos, X; notes trails as the newest source.
var crawlerFactories = []func() trawlkit.Crawler{
	func() trawlkit.Crawler { return imessage.New() },
	func() trawlkit.Crawler { return whatsapp.New() },
	func() trawlkit.Crawler { return telegram.New() },
	func() trawlkit.Crawler { return gmail.New() },
	func() trawlkit.Crawler { return calendar.New() },
	func() trawlkit.Crawler { return contacts.New() },
	func() trawlkit.Crawler { return photos.New() },
	func() trawlkit.Crawler { return twitter.New() },
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
