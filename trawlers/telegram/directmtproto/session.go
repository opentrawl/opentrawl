// Package directmtproto signs in directly to Telegram and archives cloud chats.
package directmtproto

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/coder/websocket"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
)

const SessionFilename = "mtproto-session.json"

type ClientOptions struct {
	APIID      int
	APIHash    string
	HTTPClient *http.Client
}

func NetworkHosts() []string {
	var hosts []string
	for _, endpoint := range dcs.Prod().Domains {
		host, _ := url.Parse(endpoint)
		hosts = append(hosts, host.Hostname()+":443")
	}
	sort.Strings(hosts)
	return hosts
}

func newClient(sessionPath string, options ClientOptions, dispatcher telegram.UpdateHandler) (*telegram.Client, error) {
	if environmentAPIID := os.Getenv("OPENTRAWL_TELEGRAM_API_ID"); environmentAPIID != "" {
		var err error
		options.APIID, err = strconv.Atoi(environmentAPIID)
		if err != nil || options.APIID <= 0 {
			return nil, errors.New("invalid OPENTRAWL_TELEGRAM_API_ID")
		}
	}
	if environmentAPIHash := os.Getenv("OPENTRAWL_TELEGRAM_API_HASH"); environmentAPIHash != "" {
		options.APIHash = environmentAPIHash
	}
	if options.APIID == 0 && options.APIHash == "" {
		options.APIID, options.APIHash = telegramdesktop.TelegramDesktopAPI()
	}
	if options.APIID <= 0 || options.APIHash == "" {
		return nil, errors.New("set both Telegram api_id and api_hash")
	}

	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		return nil, err
	}
	return telegram.NewClient(options.APIID, options.APIHash, telegram.Options{
		SessionStorage: privateSessionStorage{path: sessionPath},
		NoUpdates:      dispatcher == nil,
		UpdateHandler:  dispatcher,
		DialTimeout:    15 * time.Second,
		OnSelfError: func(_ context.Context, err error) error {
			if dispatcher != nil && auth.IsUnauthorized(err) {
				return nil
			}
			return err
		},
		ReconnectionBackoff: func() backoff.BackOff {
			return backoff.NewExponentialBackOff(
				backoff.WithMaxElapsedTime(30*time.Second),
				backoff.WithMaxInterval(3*time.Second),
			)
		},
		Resolver: dcs.Websocket(dcs.WebsocketOptions{DialOptions: &websocket.DialOptions{
			HTTPClient:   options.HTTPClient,
			Subprotocols: []string{"binary"},
		}}),
		Device: telegram.DeviceConfig{
			DeviceModel:    "OpenTrawl",
			AppVersion:     "OpenTrawl",
			SystemLangCode: "en",
			LangCode:       "en",
		},
	}), nil
}

type privateSessionStorage struct{ path string }

func (storage privateSessionStorage) LoadSession(ctx context.Context) ([]byte, error) {
	return (&session.FileStorage{Path: storage.path}).LoadSession(ctx)
}

func (storage privateSessionStorage) StoreSession(_ context.Context, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(storage.path), ".mtproto-session-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), storage.path)
}

func StartPhoneLogin(ctx context.Context, sessionPath, phone string, options ClientOptions) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := newClient(sessionPath+".pending", options, nil)
	if err != nil {
		return "", err
	}
	var phoneCodeHash string
	err = runTelegramSession(ctx, client, func(ctx context.Context) error {
		response, err := client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
		if err != nil {
			return err
		}
		switch sent := response.(type) {
		case *tg.AuthSentCode:
			phoneCodeHash = sent.PhoneCodeHash
			return nil
		case *tg.AuthSentCodeSuccess:
			_, err := client.Self(ctx)
			return err
		default:
			return errors.New("telegram requires a sign-in step OpenTrawl does not support")
		}
	})
	if err == nil && phoneCodeHash == "" {
		err = publishAuthorizedSession(sessionPath)
	}
	return phoneCodeHash, err
}

func CompletePhoneLogin(ctx context.Context, sessionPath, phone, phoneCodeHash, code, password2FA string, options ClientOptions) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := os.Stat(sessionPath + ".pending"); err != nil {
		return err
	}
	client, err := newClient(sessionPath+".pending", options, nil)
	if err != nil {
		return err
	}
	err = runTelegramSession(ctx, client, func(ctx context.Context) error {
		_, err := client.Auth().SignIn(ctx, phone, code, phoneCodeHash)
		if errors.Is(err, auth.ErrPasswordAuthNeeded) && password2FA != "" {
			_, err = client.Auth().Password(ctx, password2FA)
		}
		if err != nil {
			return err
		}
		_, err = client.Self(ctx)
		return err
	})
	if err != nil {
		return err
	}
	return publishAuthorizedSession(sessionPath)
}

// StartQRLogin delivers each refreshed QR URL to show and returns after approval.
func StartQRLogin(ctx context.Context, sessionPath, password2FA string, options ClientOptions, show func(string) error) error {
	dispatcher := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(dispatcher)
	client, err := newClient(sessionPath+".pending", options, dispatcher)
	if err != nil {
		return err
	}
	err = runTelegramSession(ctx, client, func(ctx context.Context) error {
		_, err := client.QR().Auth(ctx, loggedIn, func(_ context.Context, token qrlogin.Token) error {
			return show(token.URL())
		})
		if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") && password2FA != "" {
			_, err = client.Auth().Password(ctx, password2FA)
		}
		if err != nil {
			return err
		}
		_, err = client.Self(ctx)
		return err
	})
	if err != nil {
		return err
	}
	return publishAuthorizedSession(sessionPath)
}

func publishAuthorizedSession(sessionPath string) error {
	// Link refuses to replace a working session if another login finished first.
	if err := os.Link(sessionPath+".pending", sessionPath); err != nil {
		return err
	}
	return os.Remove(sessionPath + ".pending")
}

func runTelegramSession(ctx context.Context, client *telegram.Client, action func(context.Context) error) error {
	completed := false
	err := client.Run(ctx, func(ctx context.Context) error {
		err := action(ctx)
		completed = err == nil
		return err
	})
	if err != nil {
		return err
	}
	if !completed {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("the Telegram connection closed before the operation finished")
	}
	return nil
}

// Disconnect revokes the direct session and discards unfinished login state.
func Disconnect(ctx context.Context, sessionPath string, options ClientOptions) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := os.Stat(sessionPath); err == nil {
		client, err := newClient(sessionPath, options, nil)
		if err != nil {
			return err
		}
		err = runTelegramSession(ctx, client, func(ctx context.Context) error {
			_, err := client.API().AuthLogOut(ctx)
			return err
		})
		if err != nil && !tgerr.Is(err, "AUTH_KEY_UNREGISTERED", "SESSION_REVOKED") {
			return err
		}
		if err := os.Remove(sessionPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(sessionPath + ".pending"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
