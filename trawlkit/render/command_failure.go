package render

import (
	"errors"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
)

// A command fails in one of four ways. The library words every failure: one
// plain sentence that says what broke, never how to repair it. The run log
// says why.
type CommandFailureKind int

const (
	// The command line itself is wrong: an unknown command, a missing or
	// malformed argument or flag.
	CommandFailureKindCommandIsWrong CommandFailureKind = iota + 1
	// Nothing matched, or several things matched where one was needed.
	CommandFailureKindNothingOrSeveralThingsMatched
	// The link does not name a record that exists.
	CommandFailureKindLinkIsUnknown
	// An archive or the operation itself failed.
	CommandFailureKindArchiveOrOperationFailed
)

const (
	processExitCodeForFailedCommand    = 1
	processExitCodeForWrongCommandLine = 2
)

// CommandFailure is a failure the library has worded. Only this package can
// make one, so every failure sentence lives here.
type CommandFailure struct {
	commandFailureKind                   CommandFailureKind
	commandFailureSentence               string
	possiblyMatchingPeopleInDisplayOrder []*federation.FederatedPersonMatchCandidate
	nextCommandsAfterFailure             []NextCommand
	photosStepsNotBuilt                  []photosstep.PhotosStepName
}

// WithHelpCommand ends a failure with the help for the command the reader
// was typing, such as trawl search --help. A failure that already names its
// help keeps it.
func (failure CommandFailure) WithHelpCommand(trawlCommandArgumentsShowingHelp []string) CommandFailure {
	for _, nextCommand := range failure.nextCommandsAfterFailure {
		if nextCommand.nextCommandLabel == nextCommandLabelHelp {
			return failure
		}
	}
	failure.nextCommandsAfterFailure = append(
		append([]NextCommand(nil), failure.nextCommandsAfterFailure...),
		nextCommandWithLabel(nextCommandLabelHelp, trawlCommandArgumentsShowingHelp...),
	)
	return failure
}

func (failure CommandFailure) Error() string {
	return failure.commandFailureSentence
}

// ProcessExitCode is 2 when the command line is wrong and 1 otherwise.
func (failure CommandFailure) ProcessExitCode() int {
	if failure.commandFailureKind == CommandFailureKindCommandIsWrong {
		return processExitCodeForWrongCommandLine
	}
	return processExitCodeForFailedCommand
}

// CommandFailureKind reports which of the four kinds of failure this is.
func (failure CommandFailure) CommandFailureKind() CommandFailureKind {
	return failure.commandFailureKind
}

func commandIsWrong(sentence string) CommandFailure {
	return CommandFailure{commandFailureKind: CommandFailureKindCommandIsWrong, commandFailureSentence: sentence}
}

func nothingOrSeveralThingsMatched(sentence string) CommandFailure {
	return CommandFailure{commandFailureKind: CommandFailureKindNothingOrSeveralThingsMatched, commandFailureSentence: sentence}
}

func linkIsUnknown(sentence string) CommandFailure {
	return CommandFailure{commandFailureKind: CommandFailureKindLinkIsUnknown, commandFailureSentence: sentence}
}

func archiveOrOperationFailed(sentence string) CommandFailure {
	return CommandFailure{commandFailureKind: CommandFailureKindArchiveOrOperationFailed, commandFailureSentence: sentence}
}

// ErrorDescription lets a trawler return a library-worded failure across the
// child process wire; the code tells the CLI which kind it is.
func (failure CommandFailure) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:                trawlerErrorCodeForCommandFailureKind[failure.commandFailureKind],
		Message:             failure.commandFailureSentence,
		PhotosStepsNotBuilt: failure.photosStepsNotBuilt,
	}
}

var trawlerErrorCodeForCommandFailureKind = map[CommandFailureKind]string{
	CommandFailureKindCommandIsWrong:                "usage",
	CommandFailureKindNothingOrSeveralThingsMatched: "not_found",
	CommandFailureKindLinkIsUnknown:                 "unknown_short_ref",
	CommandFailureKindArchiveOrOperationFailed:      "command_failed",
}

// AsCommandFailure finds the library-worded failure inside err.
func AsCommandFailure(err error) (CommandFailure, bool) {
	var failure CommandFailure
	if errors.As(err, &failure) {
		return failure, true
	}
	return CommandFailure{}, false
}

// WriteCommandFailure writes a failure on standard error. An error the
// library did not word is reported as a failed command; its detail belongs
// in the run log, not on screen.
func (output TrawlCommandOutput) WriteCommandFailure(err error) error {
	failure, worded := AsCommandFailure(err)
	if !worded {
		failure = CommandDidNotComplete()
	}
	writer := output.standardError
	if err := writeLines(writer, Wrap(failure.commandFailureSentence, OutputWidth(writer))); err != nil {
		return err
	}
	if len(failure.possiblyMatchingPeopleInDisplayOrder) > 0 {
		if err := writeLine(writer, ""); err != nil {
			return err
		}
		if err := writePossiblyMatchingPeople(writer, failure.possiblyMatchingPeopleInDisplayOrder); err != nil {
			return err
		}
	}
	return writeNextCommands(writer, failure.nextCommandsAfterFailure)
}

// ProcessExitCodeForCommandFailure maps any failure to the process exit code.
func ProcessExitCodeForCommandFailure(err error) int {
	if failure, worded := AsCommandFailure(err); worded {
		return failure.ProcessExitCode()
	}
	return processExitCodeForFailedCommand
}

// The command line is wrong.

// UnknownCommand names the command the reader most likely meant, when one
// of knownCommandNames is a near spelling of what they typed.
func UnknownCommand(commandWords string, knownCommandNames []string) CommandFailure {
	return commandIsWrong("Unknown command " + quotedForSentence(commandWords) + "." +
		didYouMeanSentence(commandWords, knownCommandNames))
}

func UnknownTrawlerCommand(trawlerDisplayName string, commandWords string, knownTrawlerCommandNames []string) CommandFailure {
	return commandIsWrong("Unknown " + trawlerDisplayName + " command " + quotedForSentence(commandWords) + "." +
		didYouMeanSentence(commandWords, knownTrawlerCommandNames))
}

// UnknownTrawler names the trawlers there are, and the nearest spelling.
func UnknownTrawler(trawlerName string, knownTrawlerNames []string) CommandFailure {
	sentence := "Unknown trawler " + quotedForSentence(trawlerName) + "." + didYouMeanSentence(trawlerName, knownTrawlerNames)
	if len(knownTrawlerNames) > 0 {
		sentence += " Trawlers: " + strings.Join(knownTrawlerNames, ", ") + "."
	}
	return commandIsWrong(sentence)
}

func CommandMustComeBeforeItsOptions() CommandFailure {
	return commandIsWrong("The command must come before its options.")
}

func TrawlerDoesNotSupportOperation(trawlerDisplayName string, operation federation.SharedTrawlerOperation) CommandFailure {
	return archiveOrOperationFailed(trawlerDisplayName + " does not support " + strings.ToLower(strings.TrimPrefix(operation.String(), "SHARED_TRAWLER_OPERATION_")) + ".")
}

func CommandHasTooManyArguments() CommandFailure {
	return commandIsWrong("The command has too many arguments.")
}

func UnknownOption(optionName string) CommandFailure {
	return commandIsWrong("Unknown option --" + strings.TrimLeft(optionName, "-") + ".")
}

func OptionNeedsValue(optionName string) CommandFailure {
	return commandIsWrong("--" + strings.TrimLeft(optionName, "-") + " needs a value.")
}

func LimitMustBeWholeNumber() CommandFailure {
	return commandIsWrong("--limit must be a whole number.")
}

func LimitMustBeAtLeastOne() CommandFailure {
	return commandIsWrong("--limit must be at least 1.")
}

func DateOptionNeedsTime(optionName string) CommandFailure {
	return commandIsWrong("--" + strings.TrimLeft(optionName, "-") +
		" needs a date or time, such as 2026-10-04, 2026-10-04 21:00 or 2026-10-04 21:00 +02:00 (19:00 UTC).")
}

func AfterMustNotBeLaterThanBefore() CommandFailure {
	return commandIsWrong("--after must not be later than --before.")
}

func SearchNeedsWordsPersonOrDates() CommandFailure {
	return commandIsWrong("Search needs words, a person, or a date range.")
}

func MessagesNeedsConversationLink() CommandFailure {
	return commandIsWrong("Messages needs a conversation link.")
}

func OpenNeedsLink() CommandFailure {
	return commandIsWrong("Open needs a link.")
}

func WhoNeedsName() CommandFailure {
	return commandIsWrong("Who needs a name.")
}

func ConversationOptionNeedsConversationLink() CommandFailure {
	return commandIsWrong("--conversation needs a conversation link.")
}

func ConversationLinkIsForAnotherTrawler() CommandFailure {
	return commandIsWrong("The conversation link is for another trawler.")
}

func ParticipantsNeedsConversationLink() CommandFailure {
	return commandIsWrong("--participants needs a conversation link.")
}

func TrawlerUpdateOptionsNeedOneTrawler() CommandFailure {
	return commandIsWrong("Update options for one trawler need exactly one trawler.")
}

// CommandNeedsArguments names what is missing: "Versions needs a link."
func CommandNeedsArguments(commandWords string, missingArgumentPlaceholders []string) CommandFailure {
	missingArguments := make([]string, 0, len(missingArgumentPlaceholders))
	for _, placeholder := range missingArgumentPlaceholders {
		placeholder = strings.ToLower(strings.ReplaceAll(strings.Trim(placeholder, "[]"), "_", " "))
		missingArguments = append(missingArguments, "a "+placeholder)
	}
	return commandIsWrong(displayLabel(commandWords) + " needs " + joinedWithAnd(missingArguments) + ".")
}

func LinkIsNotValid() CommandFailure {
	return commandIsWrong("The link is not valid.")
}

// CommandLineRejectedByParser keeps a command-line parser's own explanation
// for a mistake the library has no sentence for.
func CommandLineRejectedByParser(parserExplanation string) CommandFailure {
	return commandIsWrong(sentenceFromParserExplanation(parserExplanation))
}

// Nothing matched, or several things did.

// NoPersonMatched lists close spellings, if any, and ends with the same
// command for the first of them.
func NoPersonMatched(
	personQuery string,
	closeSpellings []*federation.FederatedPersonMatchCandidate,
	trawlCommandArgumentsForFirstCloseSpelling []string,
) CommandFailure {
	failure := nothingOrSeveralThingsMatched("No person matched " + quotedForSentence(personQuery) + ".")
	failure.possiblyMatchingPeopleInDisplayOrder = closeSpellings
	if len(closeSpellings) > 0 && len(trawlCommandArgumentsForFirstCloseSpelling) > 0 {
		failure.nextCommandsAfterFailure = []NextCommand{
			nextCommandWithLabel(nextCommandLabelTryAgain, trawlCommandArgumentsForFirstCloseSpelling...),
		}
	}
	return failure
}

// SeveralPeopleMatched lists the people a name could mean and ends with the
// same command for the first of them.
func SeveralPeopleMatched(
	personQuery string,
	matchingPeople []*federation.FederatedPersonMatchCandidate,
	trawlCommandArgumentsForFirstMatchingPerson []string,
) CommandFailure {
	failure := nothingOrSeveralThingsMatched("More than one person matched " + quotedForSentence(personQuery) + ".")
	failure.possiblyMatchingPeopleInDisplayOrder = matchingPeople
	if len(trawlCommandArgumentsForFirstMatchingPerson) > 0 {
		failure.nextCommandsAfterFailure = []NextCommand{
			nextCommandWithLabel(nextCommandLabelTryAgain, trawlCommandArgumentsForFirstMatchingPerson...),
		}
	}
	return failure
}

func NothingInTrawlersInvolvesPerson(personDisplayName string, trawlerDisplayNames []string) CommandFailure {
	return nothingOrSeveralThingsMatched(
		"Nothing in " + joinedWithOr(trawlerDisplayNames) + " involves " + personDisplayName + ".",
	)
}

// The link is unknown.

func NoRecordHasThatLink() CommandFailure {
	return linkIsUnknown("Nothing in the archives has that link.")
}

func NoTrawlerHasThatLink() CommandFailure {
	return linkIsUnknown("No trawler has that link.")
}

// An archive or the operation failed.

func CommandDidNotComplete() CommandFailure {
	return archiveOrOperationFailed("The command did not complete.")
}

func CommandDidNotCompleteForTrawler(trawlerDisplayName string) CommandFailure {
	return archiveOrOperationFailed("The command did not complete for " + trawlerDisplayName + ".")
}

func TrawlerArchiveIsNotAvailable(trawlerDisplayName string) CommandFailure {
	return archiveOrOperationFailed("The " + trawlerDisplayName + " archive is not available.")
}

func NoTrawlersAreAvailable() CommandFailure {
	return archiveOrOperationFailed("No trawlers are available.")
}

// CommandFailureFromTrawler keeps the sentence a trawler wrote for its own
// failure, under the kind its error code names.
func CommandFailureFromTrawler(kind CommandFailureKind, trawlerSentence string) CommandFailure {
	return CommandFailure{commandFailureKind: kind, commandFailureSentence: sentenceFromParserExplanation(trawlerSentence)}
}

// WriteTrawlerOperationFailures writes one line on standard error for each
// trawler that failed or did not run while the rest of a command succeeded.
// Archive availability is left out where the output already shows it.
func (output TrawlCommandOutput) WriteTrawlerOperationFailures(
	failures []*federation.TrawlerOperationFailure,
	skippedTrawlers []*federation.TrawlerSkippedFromOperation,
	outputAlreadyShowsArchiveAvailability bool,
) error {
	writer := output.standardError
	reportedTrawlerDisplayNames := make(map[string]struct{}, len(failures)+len(skippedTrawlers))
	for _, failure := range failures {
		trawlerDisplayName := strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName())
		if failure == nil || trawlerDisplayName == "" {
			continue
		}
		reportedTrawlerDisplayNames[strings.ToLower(trawlerDisplayName)] = struct{}{}
		sentence := CommandDidNotCompleteForTrawler(trawlerDisplayName).commandFailureSentence
		if len(failure.GetPhotosStepsNotBuilt()) > 0 {
			sentence = photosStepsNotBuiltSentence(failure.GetPhotosStepsNotBuilt())
		}
		if trawlerOperationFailureMeansArchiveIsUnavailable(failure.GetFailureCode()) {
			if outputAlreadyShowsArchiveAvailability {
				continue
			}
			sentence = TrawlerArchiveIsNotAvailable(trawlerDisplayName).commandFailureSentence
		}
		if err := writeLines(writer, Wrap(sentence, OutputWidth(writer))); err != nil {
			return err
		}
	}
	for _, skippedTrawler := range skippedTrawlers {
		trawlerDisplayName := strings.TrimSpace(skippedTrawler.GetRegisteredTrawlerDisplayName())
		if skippedTrawler == nil || trawlerDisplayName == "" {
			continue
		}
		if _, alreadyReported := reportedTrawlerDisplayNames[strings.ToLower(trawlerDisplayName)]; alreadyReported {
			continue
		}
		if err := writeLines(writer, Wrap("The command did not run for "+trawlerDisplayName+".", OutputWidth(writer))); err != nil {
			return err
		}
	}
	return nil
}

func trawlerOperationFailureMeansArchiveIsUnavailable(failureCode federation.FailureCode) bool {
	return failureCode == federation.FailureCode_FAILURE_CODE_UNAVAILABLE ||
		failureCode == federation.FailureCode_FAILURE_CODE_PERMISSION
}

func joinedWithOr(values []string) string {
	return joinedWithConjunction(values, " or ")
}

func joinedWithAnd(values []string) string {
	return joinedWithConjunction(values, " and ")
}

func joinedWithConjunction(values []string, conjunction string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	default:
		return strings.Join(values[:len(values)-1], ", ") + conjunction + values[len(values)-1]
	}
}

func quotedForSentence(value string) string {
	return strconv.Quote(strings.Join(strings.Fields(value), " "))
}

func sentenceFromParserExplanation(explanation string) string {
	explanation = strings.TrimSpace(explanation)
	if explanation == "" {
		return "The command is not valid."
	}
	if first := explanation[0]; first >= 'a' && first <= 'z' {
		explanation = string(first-('a'-'A')) + explanation[1:]
	}
	if !strings.HasSuffix(explanation, ".") {
		explanation += "."
	}
	return explanation
}
