module github.com/toksikk/gidbig

go 1.26.0

require (
	github.com/bwmarrin/discordgo v0.29.0
	github.com/dustin/go-humanize v1.0.1
	github.com/gorilla/websocket v1.5.3
	github.com/mattn/go-sqlite3 v1.14.52
	github.com/openai/openai-go/v3 v3.61.0
	github.com/simplesurance/go-ip-anonymizer v0.0.0-20200429124537-35a880f8e87d
	golang.org/x/oauth2 v0.37.0
	gopkg.in/yaml.v3 v3.0.1
	gorm.io/driver/sqlite v1.6.0
	gorm.io/gorm v1.31.2
)

require (
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

// Pin a reproducible fork revision with the Op7/Op9 handshake deadlock fix
// (PR #5), immediate DAVE Welcome activation (PR #6, regression #113), and
// bounded resume attempts (6f7dfa36). Live DAVE playback must be verified
// before deploying any pin change; see docs/discord-gateway-recovery.md.
replace github.com/bwmarrin/discordgo => github.com/yeongaori/discordgo-fork v0.0.0-20260913055947-94d3e03d65d1
