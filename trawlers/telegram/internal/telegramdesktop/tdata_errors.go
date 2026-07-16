package telegramdesktop

import "github.com/gotd/td/tgerr"

const TDataSessionRejectedRemedy = "Telegram Desktop's saved session was rejected (AUTH_KEY_UNREGISTERED). Open Telegram Desktop, let it finish connecting, then run trawl sync telegram again."

func IsTDataSessionRejected(err error) bool {
	return tgerr.Is(err, "AUTH_KEY_UNREGISTERED")
}
