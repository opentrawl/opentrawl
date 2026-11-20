module github.com/opentrawl/opentrawl/gmail

go 1.26.4

require (
	github.com/opentrawl/opentrawl/trawlkit v0.13.1
	google.golang.org/protobuf v1.36.11
)

replace github.com/opentrawl/opentrawl/trawlkit => ../../trawlkit

require (
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/mattn/go-sqlite3 v1.14.47 // indirect
	github.com/pelletier/go-toml/v2 v2.4.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)
