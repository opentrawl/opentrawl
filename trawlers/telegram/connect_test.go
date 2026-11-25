package telegram

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"
	"github.com/opentrawl/opentrawl/trawlers/telegram/directmtproto"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func TestTelegramConnectPasswordFileReadsTrimmedSecretAndRejectsInvalidInputs(t *testing.T) {
	crawler := New()
	flags := flag.NewFlagSet("connect", flag.ContinueOnError)
	crawler.bindConnectFlags(flags)

	dir := t.TempDir()
	validPath := filepath.Join(dir, "telegram-2fa.txt")
	if err := os.WriteFile(validPath, []byte("  two-step-secret \n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}
	if err := flags.Parse([]string{"--qr", "--password-file", validPath}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	password, err := resolveTelegramConnectPassword(crawler.connect)
	if err != nil {
		t.Fatalf("resolveTelegramConnectPassword(valid): %v", err)
	}
	if password != "two-step-secret" {
		t.Fatalf("password = %q, want %q", password, "two-step-secret")
	}

	emptyPath := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(emptyPath, []byte(" \n\t "), 0o600); err != nil {
		t.Fatalf("write empty password file: %v", err)
	}
	if _, err := resolveTelegramConnectPassword(telegramConnectFlags{passwordFile: emptyPath}); err == nil {
		t.Fatalf("expected error for empty --password-file")
	} else {
		failure, ok := render.AsCommandFailure(err)
		if !ok || failure.CommandFailureKind() != render.CommandFailureKindCommandIsWrong {
			t.Fatalf("empty file error = %T (%v), want CommandFailureKindCommandIsWrong", err, err)
		}
		if !strings.Contains(failure.Error(), "--password-file") || !strings.Contains(failure.Error(), "must not be empty") {
			t.Fatalf("unexpected empty file message: %q", failure.Error())
		}
	}

	missingPath := filepath.Join(dir, "does-not-exist.txt")
	if _, err := resolveTelegramConnectPassword(telegramConnectFlags{passwordFile: missingPath}); err == nil {
		t.Fatalf("expected error for missing --password-file")
	} else {
		failure, ok := render.AsCommandFailure(err)
		if !ok || failure.CommandFailureKind() != render.CommandFailureKindCommandIsWrong {
			t.Fatalf("missing file error = %T (%v), want CommandFailureKindCommandIsWrong", err, err)
		}
		if !strings.Contains(failure.Error(), "--password-file") {
			t.Fatalf("unexpected missing file message: %q", failure.Error())
		}
	}

	if _, err := resolveTelegramConnectPassword(telegramConnectFlags{
		password:     "inline",
		passwordFile: validPath,
	}); err == nil {
		t.Fatalf("expected error when both --password and --password-file are set")
	} else {
		failure, ok := render.AsCommandFailure(err)
		if !ok || failure.CommandFailureKind() != render.CommandFailureKindCommandIsWrong {
			t.Fatalf("both flags error = %T (%v), want CommandFailureKindCommandIsWrong", err, err)
		}
		if !strings.Contains(failure.Error(), "--password") || !strings.Contains(failure.Error(), "--password-file") {
			t.Fatalf("unexpected conflict message: %q", failure.Error())
		}
	}
}

func TestTelegramConnectTwoStepVerificationErrorRendersActionableMessage(t *testing.T) {
	cases := []struct {
		name         string
		rawErr       error
		wantContains []string
	}{
		{
			name:   "typed error with hint",
			rawErr: &directmtproto.TwoStepVerificationPasswordRequiredError{PasswordHint: "blue notebook"},
			wantContains: []string{
				"Telegram Two-Step Verification is enabled on this account.",
				`Password hint: "blue notebook".`,
				"--password-file FILE (or --password)",
			},
		},
		{
			name:   "typed error without hint",
			rawErr: &directmtproto.TwoStepVerificationPasswordRequiredError{},
			wantContains: []string{
				"Telegram Two-Step Verification is enabled on this account.",
				"--password-file FILE (or --password)",
			},
		},
		{
			name:   "gotd auth.ErrPasswordAuthNeeded",
			rawErr: auth.ErrPasswordAuthNeeded,
			wantContains: []string{
				"Telegram Two-Step Verification is enabled on this account.",
				"--password-file FILE (or --password)",
			},
		},
		{
			name:   "gotd SESSION_PASSWORD_NEEDED RPC error",
			rawErr: tgerr.New(401, "SESSION_PASSWORD_NEEDED"),
			wantContains: []string{
				"Telegram Two-Step Verification is enabled on this account.",
				"--password-file FILE (or --password)",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := mapTelegramConnectError(tc.rawErr)
			failure, ok := render.AsCommandFailure(mapped)
			if !ok {
				t.Fatalf("mapped error = %T (%v), want render.CommandFailure", mapped, mapped)
			}
			if got := failure.ErrorDescription().Code; got != render.TrawlerErrorCodeForLibraryWordedOperationFailure {
				t.Fatalf("error description code = %q, want %q", got, render.TrawlerErrorCodeForLibraryWordedOperationFailure)
			}
			var stdout, stderr bytes.Buffer
			out := render.NewTrawlCommandOutput(&stdout, &stderr, "trawl")
			if err := out.WriteCommandFailure(mapped); err != nil {
				t.Fatalf("WriteCommandFailure: %v", err)
			}
			rendered := stderr.String()
			for _, want := range tc.wantContains {
				if !strings.Contains(rendered, want) {
					t.Fatalf("rendered stderr %q does not contain %q", rendered, want)
				}
			}
		})
	}
}

func TestTelegramDisconnectRendersTruthfulHelpNextCommand(t *testing.T) {
	response := telegramConnectionChange(command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_DISCONNECTED)
	var stdout, stderr bytes.Buffer
	out := render.NewTrawlCommandOutput(&stdout, &stderr, "trawl")
	if err := out.WriteTrawlerCommandResponse(response, nil, render.TrawlerCommandRenderContext{}); err != nil {
		t.Fatalf("WriteTrawlerCommandResponse: %v", err)
	}
	rendered := stdout.String()
	if !strings.Contains(rendered, "Telegram disconnected") {
		t.Fatalf("rendered output missing disconnect title: %q", rendered)
	}
	if !strings.Contains(rendered, "Help: trawl telegram connect --help") {
		t.Fatalf("rendered output missing truthful help hint: %q", rendered)
	}
	if strings.Contains(rendered, "Connect: trawl telegram connect") {
		t.Fatalf("rendered output still contains broken bare connect hint: %q", rendered)
	}
}
