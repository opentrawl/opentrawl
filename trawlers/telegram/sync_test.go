package telecrawl

import (
	"strings"
	"testing"

	"github.com/gotd/td/tgerr"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
)

func TestSyncImportErrorSurfacesPostboxSessionRejectedRemedy(t *testing.T) {
	err := syncImportError(telegramdesktop.PostboxSessionRejectedError{
		Err: tgerr.New(401, "AUTH_KEY_UNREGISTERED"),
	})
	command, ok := err.(commandError)
	if !ok {
		t.Fatalf("error = %T, want commandError", err)
	}
	body := command.ErrorBody()
	if body.Code != "telegram_session" {
		t.Fatalf("code = %q, want telegram_session", body.Code)
	}
	if !strings.Contains(body.Message, "AUTH_KEY_UNREGISTERED") {
		t.Fatalf("message = %q, want AUTH_KEY_UNREGISTERED", body.Message)
	}
	if body.Remedy != telegramdesktop.PostboxSessionRejectedRemedy {
		t.Fatalf("remedy = %q, want %q", body.Remedy, telegramdesktop.PostboxSessionRejectedRemedy)
	}
}
