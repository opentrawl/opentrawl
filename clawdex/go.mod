module github.com/openclaw/clawdex

go 1.26.4

replace github.com/openclaw/crawlkit => ../crawlkit

require (
	github.com/alecthomas/kong v1.15.0
	github.com/google/uuid v1.6.0
	github.com/mattn/go-sqlite3 v1.14.47
	github.com/openclaw/crawlkit v0.5.2
	github.com/pelletier/go-toml/v2 v2.4.2
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	golang.org/x/sys v0.46.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
