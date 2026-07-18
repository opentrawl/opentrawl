package telegramdesktop

import "github.com/gotd/td/tgerr"

const TelegramSessionRejectedRemedy = "Telegram for macOS's saved session was rejected (AUTH_KEY_UNREGISTERED). Open Telegram, let it finish connecting, then run trawl sync telegram again."

func IsTelegramSessionRejected(err error) bool {
	return tgerr.Is(err, "AUTH_KEY_UNREGISTERED")
}
