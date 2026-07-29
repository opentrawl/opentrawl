package telegramdesktop

import "github.com/gotd/td/tgerr"

func IsTelegramSessionRejected(err error) bool {
	return tgerr.Is(err, "AUTH_KEY_UNREGISTERED")
}
