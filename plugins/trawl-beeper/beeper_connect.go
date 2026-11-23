package beeper

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/plugins/trawl-beeper/internal/beeperapi"
	"github.com/opentrawl/opentrawl/trawlkit"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const maxTokenFileBytes = 16 * 1024

type beeperConnectFlagValues struct {
	token    string
	filePath string
}

func (t *BeeperTrawler) bindConnectFlags(fs *flag.FlagSet) {
	t.connectFlags = beeperConnectFlagValues{}
	fs.StringVar(&t.connectFlags.token, "token", "", "Beeper Desktop read-only Bearer token")
	fs.StringVar(&t.connectFlags.filePath, "file", "", "Read the Beeper Desktop read-only Bearer token from a local file")
}

func (t *BeeperTrawler) runConnect(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	token, err := resolveTokenFromConnectFlags(t.connectFlags)
	if err != nil {
		return nil, usageFailure(err)
	}
	if req.RecordSecretMutation == nil {
		return nil, render.OpenTrawlBuildCannotUseKeychain()
	}

	client, err := beeperapi.NewBeeperDesktopClient(
		t.cfg.EffectiveAPIBaseURL(),
		token,
		req.PluginHTTPClient,
		t.cfg.EffectiveRequestTimeout(),
	)
	if err != nil {
		return nil, usageFailure(err)
	}

	accounts, err := client.ListAccounts(ctx)
	if err != nil {
		return nil, classifyBeeperAPIError(err)
	}

	if err := req.RecordSecretMutation(beeperAccessTokenSecretKey, []byte(token), false); err != nil {
		return nil, fmt.Errorf("save beeper access token in keychain: %w", err)
	}

	connectedIdentifier := summarizeConnectedBeeperAccounts(accounts)
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("beeper_connected", fmt.Sprintf("accounts=%d", len(accounts)))
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_AccountConnectionChangeResponse{
			AccountConnectionChangeResponse: &command.AccountConnectionChangeResponse{
				AccountConnectionChangeKind: command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_CONNECTED,
				TrawlerCommandName:          beeperTrawlerIdentifier,
				TrawlerDisplayName:          beeperTrawlerDisplayName,
				AccountProviderDisplayName:  beeperProviderDisplayName,
				ConnectedAccountIdentifier:  connectedIdentifier,
			},
		},
	}, nil
}

func (t *BeeperTrawler) runDisconnect(_ context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if req.RecordSecretMutation == nil {
		return nil, render.OpenTrawlBuildCannotUseKeychain()
	}
	if err := req.RecordSecretMutation(beeperAccessTokenSecretKey, nil, true); err != nil {
		return nil, fmt.Errorf("delete beeper access token from keychain: %w", err)
	}
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("beeper_disconnected", "archive_kept=true")
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_AccountConnectionChangeResponse{
			AccountConnectionChangeResponse: &command.AccountConnectionChangeResponse{
				AccountConnectionChangeKind: command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_DISCONNECTED,
				TrawlerCommandName:          beeperTrawlerIdentifier,
				TrawlerDisplayName:          beeperTrawlerDisplayName,
				AccountProviderDisplayName:  beeperProviderDisplayName,
				LocalArchiveKeptOnMac:       true,
			},
		},
	}, nil
}

func resolveTokenFromConnectFlags(flags beeperConnectFlagValues) (string, error) {
	token := strings.TrimSpace(flags.token)
	filePath := strings.TrimSpace(flags.filePath)
	if token != "" && filePath != "" {
		return "", render.OptionsCannotBeUsedTogether("token", "file")
	}
	if filePath != "" {
		loaded, err := readTokenFile(filePath)
		if err != nil {
			return "", err
		}
		token = loaded
	}
	if token == "" {
		return "", errors.New("provide a Beeper Desktop read-only Bearer token with --token or --file PATH")
	}
	return token, nil
}

func (t *BeeperTrawler) resolveBeeperAccessToken(req *trawlkit.TrawlerCommandExecutionRequest, explicitFilePath string) (string, error) {
	if req != nil {
		if brokered := strings.TrimSpace(string(req.BrokeredSecrets[beeperAccessTokenSecretKey])); brokered != "" {
			return brokered, nil
		}
	}
	if trimmedFile := strings.TrimSpace(explicitFilePath); trimmedFile != "" {
		return readTokenFile(trimmedFile)
	}
	if configuredFile := strings.TrimSpace(t.cfg.TokenFile); configuredFile != "" {
		return readTokenFile(configuredFile)
	}
	return "", render.TrawlerIsNotConnected(beeperTrawlerDisplayName)
}

func readTokenFile(rawPath string) (string, error) {
	expanded := expandHomeRelativePath(rawPath)
	file, err := os.Open(expanded)
	if err != nil {
		return "", fmt.Errorf("read Beeper token file: %w", err)
	}
	defer func() { _ = file.Close() }()

	rawBytes, err := io.ReadAll(io.LimitReader(file, maxTokenFileBytes))
	if err != nil {
		return "", fmt.Errorf("read Beeper token file: %w", err)
	}
	token := strings.TrimSpace(string(rawBytes))
	if token == "" {
		return "", errors.New("beeper token file is empty")
	}
	return token, nil
}

func summarizeConnectedBeeperAccounts(accounts []beeperapi.BeeperAccount) string {
	seen := make(map[string]struct{}, len(accounts))
	var networks []string
	for _, acc := range accounts {
		name := resolveAccountNetworkName(acc)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		networks = append(networks, name)
	}
	if len(networks) == 0 {
		return beeperDefaultSelfHostedHost
	}
	return strings.Join(networks, ", ")
}

func resolveAccountNetworkName(acc beeperapi.BeeperAccount) string {
	return firstNonEmpty(acc.Network, acc.Bridge.Network, acc.Bridge.Name, acc.AccountID)
}
