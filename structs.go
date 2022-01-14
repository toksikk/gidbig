package gidbig

// FROM webserver.go
// soundItem is used to represent a sound of our COLLECTIONS for html generation
type soundItem struct {
	Itemprefix    string
	Itemcommand   string
	Itemsoundname string
	Itemtext      string
	Itemshorttext string
}

// FROM cmd.go
type Config struct {
	Token       string `yaml:"token"`
	Shard       string `yaml:"shard"`
	ShardCount  string `yaml:"shardcount"`
	Owner       string `yaml:"owner"`
	Port        int    `yaml:"port"`
	RedirectURL string `yaml:"redirecturl"`
	Ci          int    `yaml:"ci"`
	Cs          string `yaml:"cs"`
}

// Play represents an individual use of the !airhorn command
type Play struct {
	GuildID   string
	ChannelID string
	UserID    string
	Sound     *soundClip

	// The next play to occur after this, only used for chaining sounds like anotha
	Next *Play

	// If true, this was a forced play using a specific airhorn sound name
	Forced bool
}

// soundCollection of sound clips
type soundCollection struct {
	Prefix     string
	Commands   []string
	Sounds     []*soundClip
	ChainWith  *soundCollection
	soundRange int
}

// soundClip represents a sound clip
type soundClip struct {
	Name string

	// Weight adjust how likely it is this song will play, higher = more likely
	Weight int

	// Delay (in milliseconds) for the bot to wait before sending the disconnect request
	PartDelay int

	// Buffer to store encoded PCM packets
	buffer [][]byte
}
