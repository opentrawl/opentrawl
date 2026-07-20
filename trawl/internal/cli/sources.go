package cli

import (
	"os"
	"strings"

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

const allSourcesEnvironmentKey = "OPENTRAWL_ALL_SOURCES"

type crawlerRegistration struct {
	factory func() trawlkit.Crawler
	beta    bool
}

// crawlerFactories is the single source eligibility and ordering authority.
// Every human command and private app operation consumes registeredCrawlers,
// so beta visibility cannot drift between help, status, search, sync,
// namespaces, crawler wire, and AppWire. The explicit environment override is
// for local development of sources outside the beta promise.
var crawlerFactories = []crawlerRegistration{
	{factory: func() trawlkit.Crawler { return imessage.New() }, beta: true},
	{factory: func() trawlkit.Crawler { return whatsapp.New() }, beta: true},
	{factory: func() trawlkit.Crawler { return telegram.New() }, beta: true},
	{factory: func() trawlkit.Crawler { return notes.New() }, beta: true},
	{factory: func() trawlkit.Crawler { return contacts.New() }, beta: true},
	{factory: func() trawlkit.Crawler { return gmail.New() }},
	{factory: func() trawlkit.Crawler { return calendar.New() }},
	{factory: func() trawlkit.Crawler { return photos.New() }},
	{factory: func() trawlkit.Crawler { return twitter.New() }},
}

func registeredCrawlers() []trawlkit.Crawler {
	sources := make([]trawlkit.Crawler, 0, len(crawlerFactories))
	allSources := strings.TrimSpace(os.Getenv(allSourcesEnvironmentKey)) == "1"
	for _, registration := range crawlerFactories {
		if !registration.beta && !allSources {
			continue
		}
		sources = append(sources, registration.factory())
	}
	return sources
}

func ExecuteCrawlerWire(args []string) int {
	return trawlkit.Run(args, registeredCrawlers())
}
