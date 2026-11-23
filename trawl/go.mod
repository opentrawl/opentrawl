module github.com/opentrawl/opentrawl/trawl

go 1.26.4

require (
	github.com/alecthomas/kong v1.15.0
	github.com/opentrawl/opentrawl/trawlkit v0.13.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/mattn/go-sqlite3 v1.14.47 // indirect
	github.com/pelletier/go-toml/v2 v2.4.2 // indirect
	github.com/ringsaturn/orb v0.15.0 // indirect
	github.com/ringsaturn/tzf v1.2.5 // indirect
	github.com/ringsaturn/tzf-dist v0.0.2026-c-fix1 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/tidwall/geoindex v1.7.0 // indirect
	github.com/tidwall/rtree v1.10.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	howett.net/plist v1.0.1 // indirect
)

require (
	github.com/opentrawl/opentrawl/calendar v0.0.0
	github.com/opentrawl/opentrawl/gmail v0.0.0
	github.com/opentrawl/opentrawl/trawlers/contacts v0.0.0
	github.com/opentrawl/opentrawl/trawlers/imessage v0.0.0
	github.com/opentrawl/opentrawl/trawlers/mail v0.0.0
	github.com/opentrawl/opentrawl/trawlers/photos v0.0.0
	golang.org/x/sys v0.46.0 // indirect
)

replace github.com/opentrawl/opentrawl/trawlkit => ../trawlkit

replace github.com/opentrawl/opentrawl/trawlers/contacts => ../trawlers/contacts

replace github.com/opentrawl/opentrawl/trawlers/imessage => ../trawlers/imessage

replace github.com/opentrawl/opentrawl/trawlers/photos => ../trawlers/photos

replace github.com/opentrawl/opentrawl/gmail => ../trawlers/gmail

replace github.com/opentrawl/opentrawl/calendar => ../trawlers/calendar

replace github.com/opentrawl/opentrawl/trawlers/mail => ../trawlers/mail
