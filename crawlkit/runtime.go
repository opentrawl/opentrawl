package crawlkit

import "time"

const (
	HiddenWireSubcommand = "__crawlkit_wire"
	DefaultReadTimeout   = 2 * time.Minute
	DefaultWatchdog      = 10 * time.Minute
	DefaultKillGrace     = 10 * time.Second
)
