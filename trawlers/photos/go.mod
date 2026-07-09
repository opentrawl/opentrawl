module github.com/opentrawl/opentrawl/trawlers/photos

go 1.26.4

require (
	github.com/mattn/go-sqlite3 v1.14.47
	github.com/opentrawl/opentrawl/trawlkit v0.12.3-0.20260619121233-9a636444e780
)

replace github.com/opentrawl/opentrawl/trawlkit => ../../trawlkit

require (
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/pelletier/go-toml/v2 v2.4.2 // indirect
	golang.org/x/sys v0.46.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
