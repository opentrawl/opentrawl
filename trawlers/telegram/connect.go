package telegram

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/directmtproto"
	"github.com/opentrawl/opentrawl/trawlkit"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"rsc.io/qr"
)

type telegramConnectFlags struct {
	phone, code, password, phoneCodeHash string
	qr                                   bool
}

func (c *Crawler) bindConnectFlags(flags *flag.FlagSet) {
	c.connect = telegramConnectFlags{}
	flags.StringVar(&c.connect.phone, "phone", "", "Phone number in international format")
	flags.StringVar(&c.connect.code, "code", "", "Code from Telegram")
	flags.StringVar(&c.connect.password, "password", "", "Telegram two-step verification password")
	flags.StringVar(&c.connect.phoneCodeHash, "phone-code-hash", "", "Use this pending Telegram phone-code hash")
	flags.BoolVar(&c.connect.qr, "qr", false, "Sign in with a Telegram QR code")
}

func telegramSessionPath(req *trawlkit.TrawlerCommandExecutionRequest) string {
	return filepath.Join(req.TrawlerArchivePaths.TrawlerArchiveDirectoryPath, directmtproto.SessionFilename)
}

func (c *Crawler) directClientOptions(req *trawlkit.TrawlerCommandExecutionRequest) directmtproto.ClientOptions {
	return directmtproto.ClientOptions{APIID: c.cfg.APIID, APIHash: c.cfg.APIHash, HTTPClient: req.PluginHTTPClient}
}

func (c *Crawler) connectTelegram(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	flags := c.connect
	sessionPath := telegramSessionPath(req)
	if _, err := os.Stat(sessionPath); err == nil {
		return nil, errors.New("telegram is already connected")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	hashPath := sessionPath + ".phone-code-hash"
	if flags.qr {
		if flags.phone != "" || flags.code != "" || flags.phoneCodeHash != "" {
			return nil, usageErr(errors.New("--qr cannot be combined with phone login flags"))
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		qrPath := sessionPath + ".qr.png"
		defer func() { _ = os.Remove(qrPath) }()
		err := directmtproto.StartQRLogin(ctx, sessionPath, flags.password, c.directClientOptions(req), func(qrURL string) error {
			if req.ReportTrawlerCommandProgress != nil {
				req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: "connect", Message: "Run trawl telegram login-qr in another terminal to find the QR image"})
			}
			code, err := qr.Encode(qrURL, qr.M)
			if err != nil {
				return err
			}
			return os.WriteFile(qrPath, code.PNG(), 0o600)
		})
		if err != nil {
			return nil, err
		}
	} else {
		if flags.phone == "" || flags.phone[0] != '+' {
			return nil, usageErr(errors.New("provide --phone in international format or use --qr"))
		}
		if flags.code == "" {
			hash, err := directmtproto.StartPhoneLogin(ctx, sessionPath, flags.phone, c.directClientOptions(req))
			if err != nil {
				return nil, err
			}
			if hash != "" {
				if err := os.WriteFile(hashPath, []byte(hash), 0o600); err != nil {
					return nil, err
				}
				return telegramLoginDetail("Telegram sign-in", "Run connect again with the same phone number and the code from Telegram."), nil
			}
		} else {
			if flags.phoneCodeHash == "" {
				hash, err := os.ReadFile(hashPath)
				if err != nil {
					return nil, err
				}
				flags.phoneCodeHash = string(hash)
			}
			if err := directmtproto.CompletePhoneLogin(ctx, sessionPath, flags.phone, flags.phoneCodeHash, flags.code, flags.password, c.directClientOptions(req)); err != nil {
				return nil, err
			}
		}
	}
	_ = os.Remove(hashPath)
	return telegramConnectionChange(command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_CONNECTED), nil
}

func (*Crawler) telegramLoginQR(_ context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	qrPath := telegramSessionPath(req) + ".qr.png"
	_, err := os.Stat(qrPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("no Telegram QR login is waiting for approval")
	}
	if err != nil {
		return nil, err
	}
	return telegramLoginDetail("Open this image and scan it under Devices in Telegram", qrPath), nil
}

func (c *Crawler) disconnectTelegram(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	sessionPath := telegramSessionPath(req)
	if err := directmtproto.Disconnect(ctx, sessionPath, c.directClientOptions(req)); err != nil {
		return nil, err
	}
	_ = os.Remove(sessionPath + ".phone-code-hash")
	_ = os.Remove(sessionPath + ".qr.png")
	return telegramConnectionChange(command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_DISCONNECTED), nil
}

func telegramLoginDetail(title, body string) *command.TrawlerCommandResponse {
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &command.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
					TrawlerSpecificCommandDetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
						DetailDisplayName: title,
						Body:              &presentation.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: body},
					},
				},
			},
		},
	}
}

func telegramConnectionChange(kind command.AccountConnectionChangeKind) *command.TrawlerCommandResponse {
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_AccountConnectionChangeResponse{
			AccountConnectionChangeResponse: &command.AccountConnectionChangeResponse{
				AccountConnectionChangeKind: kind,
				TrawlerCommandName:          appID,
				TrawlerDisplayName:          "Telegram",
				AccountProviderDisplayName:  "Telegram",
				LocalArchiveKeptOnMac:       kind == command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_DISCONNECTED,
			},
		},
	}
}
