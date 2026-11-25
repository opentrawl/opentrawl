// Package directmtproto signs in directly to Telegram and archives cloud chats.
package directmtproto

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramnetwork"
	"github.com/opentrawl/opentrawl/trawlkit/config"
)

const SessionFilename = "mtproto-session.json"

// TwoStepVerificationPasswordRequiredError reports that Telegram requires the
// account's Two-Step Verification password to finish signing in.
type TwoStepVerificationPasswordRequiredError struct {
	PasswordHint string
}

func (err *TwoStepVerificationPasswordRequiredError) Error() string {
	if err.PasswordHint != "" {
		return "Telegram Two-Step Verification password required (hint: " + err.PasswordHint + ")"
	}
	return "Telegram Two-Step Verification password required"
}

// TwoStepVerificationPasswordHint reports whether err means Telegram requires
// a Two-Step Verification password and returns any hint Telegram supplied.
func TwoStepVerificationPasswordHint(err error) (string, bool) {
	var required *TwoStepVerificationPasswordRequiredError
	if errors.As(err, &required) {
		return required.PasswordHint, true
	}
	if isSessionPasswordNeededError(err) {
		return "", true
	}
	return "", false
}

func isSessionPasswordNeededError(err error) bool {
	return errors.Is(err, auth.ErrPasswordAuthNeeded) || tgerr.Is(err, "SESSION_PASSWORD_NEEDED")
}

func passwordRequiredError(ctx context.Context, client *telegram.Client) error {
	var passwordHint string
	if accountPassword, err := client.API().AccountGetPassword(ctx); err == nil && accountPassword != nil {
		if hint, ok := accountPassword.GetHint(); ok {
			passwordHint = strings.TrimSpace(hint)
		}
	}
	return &TwoStepVerificationPasswordRequiredError{PasswordHint: passwordHint}
}

type ClientOptions struct {
	APIID      int
	APIHash    string
	HTTPClient *http.Client
}

// Releases can set telegramBuildAPIHash with -ldflags -X. The registered
// application ID is public. A deployment can also override it with -X.
var telegramBuildAPIID = "37429334"
var telegramBuildAPIHash string

type telegramApplicationCredentials struct {
	APIID   int    `toml:"api_id"`
	APIHash string `toml:"api_hash"`
}

func telegramDirectCredentials(sessionPath string, options ClientOptions) (telegramApplicationCredentials, error) {
	var credentials telegramApplicationCredentials
	credentialsPath := filepath.Join(filepath.Dir(sessionPath), "opentrawl-app-credentials.toml")
	if err := config.LoadTOMLFileIfPresent(credentialsPath, &credentials); err != nil {
		return credentials, errors.New("could not read Telegram application credentials")
	}
	if credentials.APIID == 0 {
		var err error
		credentials.APIID, err = strconv.Atoi(telegramBuildAPIID)
		if err != nil {
			return credentials, errors.New("invalid Telegram build application ID")
		}
	}
	if credentials.APIHash == "" {
		credentials.APIHash = telegramBuildAPIHash
	}
	if options.APIID != 0 {
		credentials.APIID = options.APIID
	}
	if options.APIHash != "" {
		credentials.APIHash = options.APIHash
	}
	if environmentAPIID := os.Getenv("OPENTRAWL_TELEGRAM_API_ID"); environmentAPIID != "" {
		var err error
		credentials.APIID, err = strconv.Atoi(environmentAPIID)
		if err != nil || credentials.APIID <= 0 {
			return credentials, errors.New("invalid OPENTRAWL_TELEGRAM_API_ID")
		}
	}
	if environmentAPIHash := os.Getenv("OPENTRAWL_TELEGRAM_API_HASH"); environmentAPIHash != "" {
		credentials.APIHash = environmentAPIHash
	}
	if credentials.APIID <= 0 || credentials.APIHash == "" {
		return credentials, errors.New("set Telegram api_id and api_hash in opentrawl-app-credentials.toml in the Telegram archive directory")
	}
	return credentials, nil
}

func newClient(sessionPath string, options ClientOptions, dispatcher telegram.UpdateHandler) (*telegram.Client, error) {
	credentials, err := telegramDirectCredentials(sessionPath, options)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		return nil, err
	}
	return telegram.NewClient(credentials.APIID, credentials.APIHash, telegram.Options{
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
		Resolver: telegramnetwork.Resolver(options.HTTPClient),
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
		if isSessionPasswordNeededError(err) {
			if password2FA == "" {
				return passwordRequiredError(ctx, client)
			}
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
		if isSessionPasswordNeededError(err) {
			if password2FA == "" {
				return passwordRequiredError(ctx, client)
			}
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
