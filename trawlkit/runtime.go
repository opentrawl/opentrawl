package trawlkit

import "time"

const (
	HiddenWireSubcommand                = "__trawlkit_wire"
	HiddenBundledPluginWorkerSubcommand = "__trawlkit_plugin_worker"
	DefaultReadTimeout                  = 2 * time.Minute
	DefaultWatchdog                     = 10 * time.Minute
	DefaultKillGrace                    = 10 * time.Second
)
