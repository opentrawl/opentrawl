package trawlkit

import personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"

func NewPersonMatchFactsFromTrawler(
	registeredTrawler *RegisteredTrawlerIdentity,
	exactPersonFilterIdentifiersObservedByTrawlerArchive []string,
	personDisplayNamesObservedByTrawlerArchive ...string,
) *personv1.PersonMatchFactsFromTrawler {
	return &personv1.PersonMatchFactsFromTrawler{
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
