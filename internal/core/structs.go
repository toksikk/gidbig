package gidbig

type templateData struct {
	Prefixes  []string
	Username  string
	AvatarURL string
}

// soundItem is used to represent a sound of a collection for html generation
type soundItem struct {
	Itemprefix    string
	Itemcommand   string
	Itemsoundname string
	Itemtext      string
	Itemshorttext string
}
