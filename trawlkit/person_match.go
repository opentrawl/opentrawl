package trawlkit

import person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"

func NewPersonMatchFactsFromTrawler(
	registeredTrawler *RegisteredTrawlerIdentity,
	exactPersonFilterIdentifiersObservedByTrawlerArchive []string,
	personDisplayNamesObservedByTrawlerArchive ...string,
) *person.PersonMatchFactsFromTrawler {
	return &person.PersonMatchFactsFromTrawler{
		RegisteredTrawler: registeredTrawler,
		ExactPersonFilterIdentifiersObservedByTrawlerArchive: append(
			[]string(nil),
			exactPersonFilterIdentifiersObservedByTrawlerArchive...,
		),
		PersonDisplayNamesObservedByTrawlerArchive: append(
			[]string(nil),
			personDisplayNamesObservedByTrawlerArchive...,
		),
	}
}
