package trawlkit

import person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"

func NewPersonIdentifierWithinTrawlerArchive(
	personIdentifierWithinTrawlerArchive string,
) *person.PersonIdentifierWithinTrawlerArchive {
	return &person.PersonIdentifierWithinTrawlerArchive{
		PersonIdentifierWithinTrawlerArchive: personIdentifierWithinTrawlerArchive,
	}
}

func NewPersonAccountIdentifiersWithinService(
	personAccountIdentifiersWithinService []string,
) []*person.PersonAccountIdentifierWithinService {
	typedPersonAccountIdentifiersWithinService := make(
		[]*person.PersonAccountIdentifierWithinService,
		0,
		len(personAccountIdentifiersWithinService),
	)
	for _, personAccountIdentifierWithinService := range personAccountIdentifiersWithinService {
		typedPersonAccountIdentifiersWithinService = append(
			typedPersonAccountIdentifiersWithinService,
			&person.PersonAccountIdentifierWithinService{
				PersonAccountIdentifierWithinService: personAccountIdentifierWithinService,
			},
		)
	}
	return typedPersonAccountIdentifiersWithinService
}

func NewExactPersonFilterIdentifiers(
	exactPersonFilterIdentifiers []string,
) []*person.ExactPersonFilterIdentifier {
	typedExactPersonFilterIdentifiers := make(
		[]*person.ExactPersonFilterIdentifier,
		0,
		len(exactPersonFilterIdentifiers),
	)
	for _, exactPersonFilterIdentifier := range exactPersonFilterIdentifiers {
		typedExactPersonFilterIdentifiers = append(
			typedExactPersonFilterIdentifiers,
			&person.ExactPersonFilterIdentifier{
				ExactPersonFilterIdentifier: exactPersonFilterIdentifier,
			},
		)
	}
	return typedExactPersonFilterIdentifiers
}
