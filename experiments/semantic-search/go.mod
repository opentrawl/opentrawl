module github.com/opentrawl/opentrawl/experiments/semantic-search

go 1.26.4

require (
	github.com/asg017/sqlite-vec-go-bindings v0.1.6
	github.com/mattn/go-sqlite3 v1.14.47
	github.com/opentrawl/opentrawl/trawlkit v0.0.0
)

require (
	github.com/pelletier/go-toml/v2 v2.4.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sys v0.46.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/opentrawl/opentrawl/trawlkit => ../../trawlkit
