package gidbig

type templateData struct {
	Prefixes  []string
	Username  string
	AvatarURL string
	// IsOwner shows the admin link in the nav. It is true only for the owner
	// with a recent enough session, so the link never appears for anyone else.
	IsOwner bool
	// Admin is the admin page payload; nil on every other page.
	Admin *adminPage
}

// soundItem is used to represent a sound of a collection for html generation
type soundItem struct {
	Itemprefix    string
	Itemcommand   string
	Itemsoundname string
	Itemtext      string
	Itemshorttext string
}
