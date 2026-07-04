module github.com/opentrawl/opentrawl/trawl

go 1.26.4

require (
	github.com/alecthomas/kong v1.15.0
	github.com/mattn/go-runewidth v0.0.24
	github.com/openclaw/crawlkit v0.0.0
)

require (
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
)

replace github.com/openclaw/crawlkit => ../crawlkit
