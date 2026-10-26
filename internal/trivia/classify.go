package trivia

import (
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// urlPattern finds http(s) links in message content.
var urlPattern = regexp.MustCompile(`https?://\S+`)

// gifHosts are link hosts whose URLs are animated GIFs rather than plain links.
var gifHosts = []string{"tenor.com", "giphy.com"}

// messageCounts is what one message adds to its author's counters. GIFs,
// pictures and links are exclusive categories: every item lands in exactly one.
type messageCounts struct {
	Words    int
	GIFs     int
	Pictures int
	Links    int
}

// classifyMessage counts words, GIFs, pictures and links in a message. Words
// exclude URLs. Each attachment, content URL and embed is counted once; an
// embed for a URL already in the content is the same item and is skipped.
func classifyMessage(m *discordgo.Message) messageCounts {
	var c messageCounts
	if m == nil {
		return c
	}
	urls := urlPattern.FindAllString(m.Content, -1)
	c.Words = len(strings.Fields(urlPattern.ReplaceAllString(m.Content, " ")))

	for _, a := range m.Attachments {
		if a == nil {
			continue
		}
		switch attachmentKind(a) {
		case kindGIF:
			c.GIFs++
		case kindPicture:
			c.Pictures++
		}
	}

	seen := make(map[string]bool, len(urls))
	isGIF := make(map[string]bool, len(urls))
	for _, u := range urls {
		seen[u] = true
		isGIF[u] = gifHost(u)
	}
	for _, e := range m.Embeds {
		if e == nil || e.URL == "" {
			continue
		}
		if seen[e.URL] {
			// The embed previews a link from the content; a gifv embed
			// reclassifies that link as a GIF.
			if e.Type == discordgo.EmbedTypeGifv {
				isGIF[e.URL] = true
			}
			continue
		}
		seen[e.URL] = true
		isGIF[e.URL] = e.Type == discordgo.EmbedTypeGifv || gifHost(e.URL)
		urls = append(urls, e.URL)
	}
	for _, u := range urls {
		if isGIF[u] {
			c.GIFs++
		} else {
			c.Links++
		}
	}
	return c
}

type itemKind int

const (
	kindOther itemKind = iota
	kindGIF
	kindPicture
)

func attachmentKind(a *discordgo.MessageAttachment) itemKind {
	ct := strings.ToLower(a.ContentType)
	ext := strings.ToLower(path.Ext(a.Filename))
	switch {
	case ct == "image/gif" || ext == ".gif":
		return kindGIF
	case strings.HasPrefix(ct, "image/"):
		return kindPicture
	case ct == "" && (ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp"):
		return kindPicture
	}
	return kindOther
}

func gifHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range gifHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".gif")
}
