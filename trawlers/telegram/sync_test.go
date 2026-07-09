package telecrawl

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gotd/td/tgerr"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
)

func TestSyncImportErrorSurfacesTDataSessionRejectedCause(t *testing.T) {
	sourceErr := fmt.Errorf("telegram session is not authorized: %w", tgerr.New(401, "AUTH_KEY_UNREGISTERED"))
	err := syncImportError(sourceErr)
	command, ok := err.(commandError)
	if !ok {
		t.Fatalf("error = %T, want commandError", err)
	}
	body := command.ErrorBody()
	if body.Code != "telegram_session" {
		t.Fatalf("code = %q, want telegram_session", body.Code)
	}
	if body.Message != sourceErr.Error() {
		t.Fatalf("message = %q, want original error %q", body.Message, sourceErr.Error())
	}
	if body.Remedy != telegramdesktop.TDataSessionRejectedRemedy {
		t.Fatalf("remedy = %q, want %q", body.Remedy, telegramdesktop.TDataSessionRejectedRemedy)
	}
	if !strings.Contains(command.Error(), sourceErr.Error()) || !strings.Contains(command.Error(), telegramdesktop.TDataSessionRejectedRemedy) {
		t.Fatalf("human error = %q, want cause and remedy", command.Error())
	}
	if !tgerr.Is(command, "AUTH_KEY_UNREGISTERED") {
		t.Fatalf("wrapped error lost AUTH_KEY_UNREGISTERED: %v", command)
	}
	t.Logf("json_error code=%q message=%q remedy=%q", body.Code, body.Message, body.Remedy)
	t.Logf("human_error=%q", command.Error())
}
