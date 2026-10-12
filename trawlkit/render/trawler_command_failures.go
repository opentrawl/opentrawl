package render

import "strings"

// Failures a trawler reports about its own records and options. A trawler
// names the kind of record; the library words the sentence.

// ArchiveRecordKindNoun is the plain word for one kind of archive record, as
// it appears in a failure sentence: "No event has that link."
type ArchiveRecordKindNoun string

const (
	ArchiveRecordKindNounMessage      ArchiveRecordKindNoun = "message"
	ArchiveRecordKindNounConversation ArchiveRecordKindNoun = "conversation"
	ArchiveRecordKindNounPost         ArchiveRecordKindNoun = "post"
	ArchiveRecordKindNounEvent        ArchiveRecordKindNoun = "event"
	ArchiveRecordKindNounCalendar     ArchiveRecordKindNoun = "calendar"
	ArchiveRecordKindNounNote         ArchiveRecordKindNoun = "note"
	ArchiveRecordKindNounNoteVersion  ArchiveRecordKindNoun = "note version"
	ArchiveRecordKindNounNoteFolder   ArchiveRecordKindNoun = "folder"
	ArchiveRecordKindNounPerson       ArchiveRecordKindNoun = "person"
)

// The command line is wrong.

func LinkIsForAnotherTrawler() CommandFailure {
	return commandIsWrong("The link is for another trawler.")
}

func LinkIsNotForArchiveRecordKind(recordKind ArchiveRecordKindNoun) CommandFailure {
	return commandIsWrong("This is not " + withIndefiniteArticle(string(recordKind)) + " link.")
}

func OptionsCannotBeUsedTogether(firstOptionName string, secondOptionName string) CommandFailure {
	return commandIsWrong(optionForSentence(firstOptionName) + " and " + optionForSentence(secondOptionName) + " cannot be used together.")
}

// OptionValueMustBeOneOf lists the values the option accepts:
// "--by must be likes, retweets or replies."
func OptionValueMustBeOneOf(optionName string, acceptedOptionValues []string) CommandFailure {
	return commandIsWrong(optionForSentence(optionName) + " must be " + joinedWithOr(acceptedOptionValues) + ".")
}

func OptionValueIsNotValid(optionName string) CommandFailure {
	return commandIsWrong("The value of " + optionForSentence(optionName) + " is not valid.")
}

func OptionMustNotBeNegative(optionName string) CommandFailure {
	return commandIsWrong(optionForSentence(optionName) + " must not be negative.")
}

func DescriptionMustNotBeEmpty() CommandFailure {
	return commandIsWrong("The description must not be empty.")
}

func TrawlerSearchNeedsWords(trawlerDisplayName string) CommandFailure {
	return commandIsWrong(trawlerDisplayName + " search needs words.")
}

// Nothing matched, or several things did.

func SeveralArchiveRecordsHaveLink(recordKind ArchiveRecordKindNoun) CommandFailure {
	return nothingOrSeveralThingsMatched("More than one " + string(recordKind) + " has that link.")
}

func NoArchiveRecordMatched(recordKind ArchiveRecordKindNoun) CommandFailure {
	return nothingOrSeveralThingsMatched("No " + string(recordKind) + " matched.")
}

func SeveralArchiveRecordsMatched(recordKind ArchiveRecordKindNoun) CommandFailure {
	return nothingOrSeveralThingsMatched("More than one " + string(recordKind) + " matched.")
}

func NoArchiveRecordHasThatName(recordKind ArchiveRecordKindNoun) CommandFailure {
	return nothingOrSeveralThingsMatched("No " + string(recordKind) + " has that name.")
}

// The link is unknown.

func NoArchiveRecordHasLink(recordKind ArchiveRecordKindNoun) CommandFailure {
	return linkIsUnknown("No " + string(recordKind) + " has that link.")
}

func optionForSentence(optionName string) string {
	return "--" + strings.TrimLeft(strings.TrimSpace(optionName), "-")
}

func withIndefiniteArticle(noun string) string {
	if noun != "" && strings.ContainsRune("aeiou", rune(noun[0])) {
		return "an " + noun
	}
	return "a " + noun
}
