package trawlkit

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

func validatedTrawlerCommandDeclarations(trawler Trawler) (map[federationv1.SharedTrawlerOperation]TrawlerCommand, error) {
	sharedCommands, err := sharedTrawlerCommandDeclarations(trawler)
	if err != nil {
		return nil, err
	}
	if err := validateBespokeTrawlerCommands(trawler); err != nil {
		return nil, err
	}
	return sharedCommands, nil
}

type trawlerCommandDeclarationError struct {
	name    string
	message string
}

func (e trawlerCommandDeclarationError) Error() string {
	return fmt.Sprintf("invalid %s TrawlerCommand declaration: %s", strings.TrimSpace(e.name), e.message)
}

func (e trawlerCommandDeclarationError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    "invalid_trawler_command_declaration",
		Message: e.Error(),
	}
}

func validateBespokeTrawlerCommands(trawler Trawler) error {
	for _, command := range trawler.TrawlerCommands() {
		if command.SharedTrawlerOperation != federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
			continue
		}
		if _, err := storeModeForTrawlerCommand(command); err != nil {
			return err
		}
	}
	return nil
}

func storeModeForTrawlerCommand(command TrawlerCommand) (storeMode, error) {
	switch command.TrawlerCommandArchiveAccess {
	case TrawlerCommandArchiveAccessDefault:
		if command.TrawlerCommandChangesArchive {
			return storeWrite, nil
		}
		return storeRead, nil
	case TrawlerCommandArchiveAccessNone:
		return storeNone, nil
	case TrawlerCommandArchiveAccessOptional:
		if command.TrawlerCommandChangesArchive {
			return 0, trawlerCommandDeclarationError{
				name:    trawlerCommandDisplayName(command),
				message: "TrawlerCommandArchiveAccessOptional cannot be used when TrawlerCommandChangesArchive is true",
			}
		}
		return storeOptional, nil
	case TrawlerCommandArchiveAccessRequired:
		if command.TrawlerCommandChangesArchive {
			return storeWrite, nil
		}
		return storeRead, nil
	default:
		return 0, trawlerCommandDeclarationError{
			name:    trawlerCommandDisplayName(command),
			message: fmt.Sprintf("TrawlerCommandArchiveAccess has unknown value %d", command.TrawlerCommandArchiveAccess),
		}
	}
}

func trawlerCommandArchiveAccessName(access TrawlerCommandArchiveAccess) string {
	switch access {
	case TrawlerCommandArchiveAccessDefault:
		return "TrawlerCommandArchiveAccessDefault"
	case TrawlerCommandArchiveAccessNone:
		return "TrawlerCommandArchiveAccessNone"
	case TrawlerCommandArchiveAccessOptional:
		return "TrawlerCommandArchiveAccessOptional"
	case TrawlerCommandArchiveAccessRequired:
		return "TrawlerCommandArchiveAccessRequired"
	default:
		return fmt.Sprintf("TrawlerCommandArchiveAccess(%d)", access)
	}
}

func trawlerCommandDisplayName(command TrawlerCommand) string {
	if sharedCommandName := sharedTrawlerOperationCommandName(command.SharedTrawlerOperation); sharedCommandName != "" {
		return sharedCommandName
	}
	name := strings.Join(strings.Fields(command.TrawlerCommandName), " ")
	if name == "" {
		return "unnamed"
	}
	return name
}
