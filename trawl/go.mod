module github.com/opentrawl/opentrawl/trawl

go 1.26.4

require (
	github.com/alecthomas/kong v1.15.0
	github.com/mattn/go-runewidth v0.0.24
	github.com/openclaw/crawlkit v0.13.1
)

require (
	github.com/andybalholm/brotli v1.2.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-faster/jx v1.2.0 // indirect
	github.com/go-faster/xor v1.0.0 // indirect
	github.com/go-faster/yaml v0.4.6 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gotd/ige v0.2.2 // indirect
	github.com/gotd/log v0.1.0 // indirect
	github.com/gotd/neo v0.1.5 // indirect
	github.com/gotd/td v0.159.0 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/mattn/go-sqlite3 v1.14.47 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/ogen-go/ogen v1.22.0 // indirect
	github.com/pelletier/go-toml/v2 v2.4.2 // indirect
	github.com/refraction-networking/utls v1.8.2 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/yuin/goldmark v1.8.2 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/exp v0.0.0-20260611194520-c48552f49976 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	golang.org/x/tools v0.46.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.53.0 // indirect
	rsc.io/qr v0.2.0 // indirect
)

require (
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/openclaw/clawdex v0.0.0
	github.com/openclaw/imsgcrawl v0.0.0
	github.com/openclaw/photoscrawl v0.0.0
	github.com/openclaw/telecrawl v0.0.0
	github.com/openclaw/wacrawl v0.0.0
	github.com/opentrawl/opentrawl/birdcrawl v0.0.0
	github.com/opentrawl/opentrawl/calcrawl v0.0.0
	github.com/opentrawl/opentrawl/gogcrawl v0.0.0
	github.com/opentrawl/opentrawl/trawlers/notes v0.0.0
	golang.org/x/sys v0.46.0 // indirect
)

replace github.com/openclaw/crawlkit => ../crawlkit

replace github.com/openclaw/clawdex => ../trawlers/contacts

replace github.com/openclaw/imsgcrawl => ../trawlers/imessage

replace github.com/openclaw/telecrawl => ../trawlers/telegram

replace github.com/openclaw/wacrawl => ../trawlers/whatsapp

replace github.com/openclaw/photoscrawl => ../trawlers/photos

replace github.com/opentrawl/opentrawl/gogcrawl => ../trawlers/gmail

replace github.com/opentrawl/opentrawl/calcrawl => ../trawlers/calendar

replace github.com/opentrawl/opentrawl/birdcrawl => ../trawlers/twitter

replace github.com/opentrawl/opentrawl/trawlers/notes => ../trawlers/notes
