package twitter

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"github.com/opentrawl/opentrawl/twitter/internal/xweb"
)

const (
	twitterDisplayName         = "Twitter"
	twitterAccountProviderName = "X"
)

type connectOptions struct {
	authToken string
	ct0       string
	fromEnv   bool
	filePath  string
}

func (r *runtime) runConnect() (*command.TrawlerCommandResponse, error) {
	authToken, ct0, err := r.resolveConnectCookies()
	if err != nil {
		return nil, err
	}
	cfg, err := loadBirdConfig(r.configPath)
	if err != nil {
		return nil, err
	}
	creds := &xweb.WebSessionCredentials{
		AuthToken: authToken,
		CT0:       ct0,
		Path:      cfg.SessionPath(),
	}
	client, err := xweb.NewClient(xweb.ClientOptions{
		Origin:      xwebOriginOverride,
		HTTPClient:  xwebHTTPClient,
		Credentials: creds,
	})
	if err != nil {
		return nil, r.webSessionError(err)
	}
	account, err := client.VerifyAccount(r.ctx)
	if err != nil {
		return nil, r.webSessionError(err)
	}

	err = r.withStore(func(st *store.Store) error {
		boundOwnerID, err := st.OwnerAuthorID(r.ctx)
		if err != nil {
			return err
		}
		if boundOwnerID != "" && boundOwnerID != account.UserID {
			return render.TrawlerArchiveBelongsToAnotherAccount(twitterDisplayName, twitterAccountProviderName)
		}
		if cfg.UserID != "" && cfg.UserID != account.UserID {
			return render.TrawlerArchiveBelongsToAnotherAccount(twitterDisplayName, twitterAccountProviderName)
		}
		if err := xweb.SaveWebSession(cfg.SessionPath(), authToken, ct0); err != nil {
			return err
		}
		if err := cfg.SaveSourceAndIdentity(TwitterSyncSourceWebSession, account.UserID, account.Username); err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := st.CommitLivePage(r.ctx, store.LivePage{
			Profiles: []store.Profile{{
				AuthorID:    account.UserID,
				Handle:      account.Username,
				DisplayName: account.DisplayName,
				LastSeenAt:  now,
			}},
			States: []store.UpdateStateUpdate{{
				Kind:         "auth:session_valid",
				Cursor:       "true",
				LastResult:   "true",
				LastUpdateAt: now,
			}},
			UpdatedAt: now,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if r.log != nil {
		_ = r.log.Info("twitter_connected", "source=web_session")
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_AccountConnectionChangeResponse{
			AccountConnectionChangeResponse: &command.AccountConnectionChangeResponse{
				AccountConnectionChangeKind: command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_CONNECTED,
				TrawlerCommandName:          appID,
				TrawlerDisplayName:          twitterDisplayName,
				AccountProviderDisplayName:  twitterAccountProviderName,
				ConnectedAccountIdentifier:  "@" + account.Username,
			},
		},
	}, nil
}

func (r *runtime) runDisconnect() (*command.TrawlerCommandResponse, error) {
	cfg, err := loadBirdConfig(r.configPath)
	if err != nil {
		return nil, err
	}
	if err := xweb.DeleteWebSession(cfg.SessionPath()); err != nil {
		return nil, err
	}
	connectedAccount := ""
	if cfg.Handle != "" {
		connectedAccount = "@" + cfg.Handle
	}
	if r.log != nil {
		_ = r.log.Info("twitter_disconnected", "archive_kept=true")
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_AccountConnectionChangeResponse{
			AccountConnectionChangeResponse: &command.AccountConnectionChangeResponse{
				AccountConnectionChangeKind: command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_DISCONNECTED,
				TrawlerCommandName:          appID,
				TrawlerDisplayName:          twitterDisplayName,
				AccountProviderDisplayName:  twitterAccountProviderName,
				ConnectedAccountIdentifier:  connectedAccount,
				LocalArchiveKeptOnMac:       true,
			},
		},
	}, nil
}

func (r *runtime) resolveConnectCookies() (string, string, error) {
	opts := r.c.connect
	authToken := strings.TrimSpace(opts.authToken)
	ct0 := strings.TrimSpace(opts.ct0)
	if opts.fromEnv {
		if authToken == "" {
			authToken = firstNonEmpty(os.Getenv("AUTH_TOKEN"), os.Getenv("TWITTER_AUTH_TOKEN"))
		}
		if ct0 == "" {
			ct0 = firstNonEmpty(os.Getenv("CT0"), os.Getenv("TWITTER_CT0"))
		}
	}
	if filePath := strings.TrimSpace(opts.filePath); filePath != "" {
		f, err := os.Open(filePath)
		if err != nil {
			return "", "", usageErr(err)
		}
		fileAuth, fileCT0, parseErr := parseCookiesFromReader(f)
		_ = f.Close()
		if parseErr != nil {
			return "", "", usageErr(parseErr)
		}
		if authToken == "" {
			authToken = fileAuth
		}
		if ct0 == "" {
			ct0 = fileCT0
		}
	}
	if authToken == "" || ct0 == "" {
		return "", "", usageErr(errors.New("provide both auth_token and ct0 via --from-env, --file PATH, or --auth-token and --ct0"))
	}
	return authToken, ct0, nil
}

func parseCookiesFromReader(reader io.Reader) (string, string, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 64*1024))
	var authToken, ct0 string
	var bareLines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		if key, rawVal, ok := strings.Cut(line, "="); ok {
			k := strings.ToLower(strings.TrimSpace(key))
			val := strings.TrimSpace(rawVal)
			if unquoted, err := strconv.Unquote(val); err == nil {
				val = unquoted
			} else {
				val = strings.Trim(val, "'\"")
			}
			switch k {
			case "auth_token", "twitter_auth_token":
				authToken = val
			case "ct0", "twitter_ct0":
				ct0 = val
			}
			continue
		}
		bareLines = append(bareLines, line)
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if authToken == "" && ct0 == "" && len(bareLines) == 2 {
		authToken = bareLines[0]
		ct0 = bareLines[1]
	}
	return authToken, ct0, nil
}

func (r *runtime) webSessionError(err error) error {
	var authErr *xweb.WebSessionAuthError
	var rateLimited *xweb.WebRateLimitedError
	switch {
	case errors.As(err, &authErr):
		if r.log != nil {
			_ = r.log.Warn("xweb_auth_failed", authErr.Error())
		}
		return render.AccountProviderNoLongerAcceptsConnection(twitterAccountProviderName, twitterDisplayName)
	case errors.As(err, &rateLimited):
		return r.contractError("rate_limited", rateLimited.Error())
	case errors.Is(err, xweb.ErrWebSessionMissing):
		return render.TrawlerIsNotConnected(twitterDisplayName)
	case errors.Is(err, xweb.ErrWebSessionIncomplete), errors.Is(err, xweb.ErrWebSessionInvalid):
		return r.contractError("credentials_missing", err.Error())
	case errors.Is(err, xweb.ErrWebSessionPermissions):
		return r.contractError("credentials_missing", "X web session file has unsafe permissions")
	default:
		return err
	}
}
