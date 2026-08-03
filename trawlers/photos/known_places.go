package photos

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"strconv"
	"strings"
	"text/template"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed known_places_text.tmpl
var configuredKnownPlacesText string

var configuredKnownPlacesTemplates, configuredKnownPlacesTemplatesError = template.New("configured-known-places").Funcs(template.FuncMap{
	"decimal": configuredKnownPlaceDecimalText,
	"kind":    configuredKnownPlaceKindText,
	"time":    configuredKnownPlaceTimeText,
}).Parse(configuredKnownPlacesText)

type configuredKnownPlaceTemplateData struct {
	Configuration *locationwire.KnownPlaceConfiguration
	Place         *locationwire.ConfiguredKnownPlace
	ArgumentName  string
}

func configuredKnownPlacesListCommand() trawlkit.TrawlerCommand {
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:               "known-places",
		TrawlerCommandHelpDescription:    "List private places used to reduce nearby-place noise",
		TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessRequired,
		TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
		ExecuteTrawlerCommand: func(ctx context.Context, request *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
			if len(request.TrawlerCommandPositionalArguments) != 0 {
				return nil, configuredKnownPlaceUsageError("known-places-usage")
			}
			configuration, err := archive.ListConfiguredKnownPlaces(ctx, request.OpenedTrawlerArchiveStore)
			if err != nil {
				return nil, err
			}
			text, err := renderConfiguredKnownPlacesText("list", configuredKnownPlaceTemplateData{Configuration: configuration})
			if err != nil {
				return nil, err
			}
			return photosDetailCommandResponse("Photos known places", photosDetailTextField("Places", text)), nil
		},
	}
}

func configuredKnownPlaceSetCommand() trawlkit.TrawlerCommand {
	var radiusMeters float64
	var validFromText string
	var validUntilText string
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:                    "set-known-place",
		TrawlerCommandHelpDescription:         "Save one private home, former home or workplace",
		TrawlerCommandPositionalArgumentNames: []string{"TYPE", "NAME", "LATITUDE", "LONGITUDE"},
		TrawlerCommandChangesArchive:          true,
		TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
		TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
		RegisterTrawlerCommandFlags: func(flagSet *flag.FlagSet) {
			flagSet.Float64Var(&radiusMeters, "radius-meters", archive.DefaultConfiguredKnownPlaceRadiusMeters, "Maximum distance from the saved coordinate")
			flagSet.StringVar(&validFromText, "valid-from", "", "Date or time when this became your place")
			flagSet.StringVar(&validUntilText, "valid-until", "", "Date or time when this stopped being your place")
		},
		ExecuteTrawlerCommand: func(ctx context.Context, request *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
			arguments := request.TrawlerCommandPositionalArguments
			if len(arguments) != 4 {
				return nil, configuredKnownPlaceUsageError("set-known-place-usage")
			}
			kind, err := parseConfiguredKnownPlaceKind(arguments[0])
			if err != nil {
				return nil, configuredKnownPlaceUsageError("known-place-type")
			}
			latitude, err := parseConfiguredKnownPlaceCoordinate(arguments[2], "latitude")
			if err != nil {
				return nil, err
			}
			longitude, err := parseConfiguredKnownPlaceCoordinate(arguments[3], "longitude")
			if err != nil {
				return nil, err
			}
			validFrom, err := parseOptionalConfiguredKnownPlaceTime(validFromText, "--valid-from")
			if err != nil {
				return nil, err
			}
			validUntil, err := parseOptionalConfiguredKnownPlaceTime(validUntilText, "--valid-until")
			if err != nil {
				return nil, err
			}
			place, err := archive.SetConfiguredKnownPlace(ctx, request.OpenedTrawlerArchiveStore, &locationwire.ConfiguredKnownPlace{
				Kind:         kind,
				DisplayName:  strings.TrimSpace(arguments[1]),
				Coordinate:   &locationwire.Coordinate{Latitude: latitude, Longitude: longitude},
				RadiusMeters: radiusMeters,
				ValidFrom:    validFrom,
				ValidUntil:   validUntil,
			})
			if err != nil {
				return nil, output.UsageError{Err: output.HumanFacingErrorMessage(err.Error())}
			}
			if err := writeConfiguredKnownPlaceLog(request, "photos_known_place_saved", "saved-log"); err != nil {
				return nil, err
			}
			text, err := renderConfiguredKnownPlacesText("saved", configuredKnownPlaceTemplateData{Place: place})
			if err != nil {
				return nil, err
			}
			return photosDetailCommandResponse("Photos known place", photosDetailTextField("Saved", text)), nil
		},
	}
}

func configuredKnownPlaceRemoveCommand() trawlkit.TrawlerCommand {
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:                    "remove-known-place",
		TrawlerCommandHelpDescription:         "Remove one private home, former home or workplace",
		TrawlerCommandPositionalArgumentNames: []string{"TYPE", "NAME"},
		TrawlerCommandChangesArchive:          true,
		TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
		TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
		ExecuteTrawlerCommand: func(ctx context.Context, request *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
			arguments := request.TrawlerCommandPositionalArguments
			if len(arguments) != 2 {
				return nil, configuredKnownPlaceUsageError("remove-known-place-usage")
			}
			kind, err := parseConfiguredKnownPlaceKind(arguments[0])
			if err != nil {
				return nil, configuredKnownPlaceUsageError("known-place-type")
			}
			selection := &locationwire.ConfiguredKnownPlace{Kind: kind, DisplayName: strings.TrimSpace(arguments[1])}
			removed, err := archive.RemoveConfiguredKnownPlace(ctx, request.OpenedTrawlerArchiveStore, selection.GetKind(), selection.GetDisplayName())
			if err != nil {
				return nil, output.UsageError{Err: output.HumanFacingErrorMessage(err.Error())}
			}
			if !removed {
				text, renderErr := renderConfiguredKnownPlacesText("not-found", configuredKnownPlaceTemplateData{Place: selection})
				if renderErr != nil {
					return nil, renderErr
				}
				return nil, output.HumanFacingErrorMessage(text)
			}
			if err := writeConfiguredKnownPlaceLog(request, "photos_known_place_removed", "removed-log"); err != nil {
				return nil, err
			}
			text, err := renderConfiguredKnownPlacesText("removed", configuredKnownPlaceTemplateData{Place: selection})
			if err != nil {
				return nil, err
			}
			return photosDetailCommandResponse("Photos known place", photosDetailTextField("Removed", text)), nil
		},
	}
}

func parseConfiguredKnownPlaceKind(value string) (locationwire.ConfiguredKnownPlaceKind, error) {
	switch strings.TrimSpace(value) {
	case "home":
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_HOME, nil
	case "former-home":
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_FORMER_HOME, nil
	case "work":
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_WORK, nil
	default:
		return locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_UNSPECIFIED, errors.New("unknown known-place type")
	}
}

func parseConfiguredKnownPlaceCoordinate(value, coordinateName string) (float64, error) {
	coordinate, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err == nil {
		return coordinate, nil
	}
	message, renderErr := renderConfiguredKnownPlacesText("coordinate", configuredKnownPlaceTemplateData{ArgumentName: coordinateName})
	if renderErr != nil {
		return 0, renderErr
	}
	return 0, output.UsageError{Err: output.HumanFacingErrorMessage(message)}
}

func parseOptionalConfiguredKnownPlaceTime(value, flagName string) (*timestamppb.Timestamp, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := flags.Date(value)
	if err == nil {
		return timestamppb.New(parsed), nil
	}
	message, renderErr := renderConfiguredKnownPlacesText("time", configuredKnownPlaceTemplateData{ArgumentName: flagName})
	if renderErr != nil {
		return nil, renderErr
	}
	return nil, output.UsageError{Err: output.HumanFacingErrorMessage(message)}
}

func configuredKnownPlaceUsageError(templateName string) error {
	message, err := renderConfiguredKnownPlacesText(templateName, configuredKnownPlaceTemplateData{})
	if err != nil {
		return err
	}
	return output.UsageError{Err: output.HumanFacingErrorMessage(message)}
}

func renderConfiguredKnownPlacesText(templateName string, data configuredKnownPlaceTemplateData) (string, error) {
	if configuredKnownPlacesTemplatesError != nil {
		return "", configuredKnownPlacesTemplatesError
	}
	var rendered strings.Builder
	if err := configuredKnownPlacesTemplates.ExecuteTemplate(&rendered, templateName, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(rendered.String()), nil
}

func configuredKnownPlaceKindText(kind locationwire.ConfiguredKnownPlaceKind) string {
	switch kind {
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_HOME:
		return "Home"
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_FORMER_HOME:
		return "Former home"
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_WORK:
		return "Work"
	default:
		return "Unknown"
	}
}

func configuredKnownPlaceDecimalText(value float64, precision int) string {
	return strconv.FormatFloat(value, 'f', precision, 64)
}

func configuredKnownPlaceTimeText(value *timestamppb.Timestamp) string {
	if value == nil {
		return ""
	}
	return value.AsTime().Format("2006-01-02")
}

func writeConfiguredKnownPlaceLog(request *trawlkit.TrawlerCommandExecutionRequest, eventName, templateName string) error {
	if request.TrawlerCommandLog == nil {
		return nil
	}
	message, err := renderConfiguredKnownPlacesText(templateName, configuredKnownPlaceTemplateData{})
	if err != nil {
		return err
	}
	return request.TrawlerCommandLog.Info(eventName, message)
}
