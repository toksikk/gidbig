package gidbig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	humanize "github.com/dustin/go-humanize"
	"github.com/toksikk/gidbig/internal/bot"
)

const (
	statusProviderTimeout = 500 * time.Millisecond
	discordMessageLimit   = 2000
	embedFieldValueLimit  = 1024
	statusWarningMaxRunes = 120
	statusRuleWidth       = 40
	statusOptionView      = "view"
	statusOptionFormat    = "format"
	statusViewSummary     = "summary"
	statusViewDetailed    = "detailed"
	statusFormatText      = "text"
	statusFormatEmbed     = "embed"
	statusEmbedColorOK    = 0x2ecc71
	statusEmbedColorWarn  = 0xe67e22
)

// statusProviders and statusDBPaths are populated during startup.
var (
	statusProviders []bot.StatsProvider
	statusDBPaths   []string
)

type moduleStatus struct {
	Name  string
	Stats bot.ModuleStats
	Err   error
}

type dbFileStatus struct {
	Path string
	Size int64
	Err  error
}

type guildStatus struct {
	Name    string
	Members int
	Voice   bool
}

type statusSnapshot struct {
	Version    string
	GoVersion  string
	Discordgo  string
	Platform   string
	Started    time.Time
	Uptime     time.Duration
	Guilds     int
	Goroutines int
	VoiceConns int
	Latency    time.Duration
	HeapAlloc  uint64
	HeapSys    uint64
	Sys        uint64
	StackInuse uint64
	NumGC      uint32
	LastGC     time.Time
	DBFiles    []dbFileStatus
	Modules    []moduleStatus
	GuildList  []guildStatus
}

func statusCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "status",
		Description: "Show bot runtime status (owner only)",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        statusOptionView,
				Description: "How much to show (default: summary)",
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "summary", Value: statusViewSummary},
					{Name: "detailed (module details + guilds)", Value: statusViewDetailed},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        statusOptionFormat,
				Description: "Output style (default: text)",
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "text (monospace, aligned)", Value: statusFormatText},
					{Name: "embed (card layout)", Value: statusFormatEmbed},
				},
			},
		},
	}
}

// statusOptions extracts view/format options, defaulting to summary text.
func statusOptions(opts []*discordgo.ApplicationCommandInteractionDataOption) (view, format string) {
	view, format = statusViewSummary, statusFormatText
	for _, o := range opts {
		switch o.Name {
		case statusOptionView:
			if o.StringValue() == statusViewDetailed {
				view = statusViewDetailed
			}
		case statusOptionFormat:
			if o.StringValue() == statusFormatEmbed {
				format = statusFormatEmbed
			}
		}
	}
	return view, format
}

func collectStatus(ctx context.Context, s *discordgo.Session, providers []bot.StatsProvider, dbPaths []string) statusSnapshot {
	mem := runtime.MemStats{}
	runtime.ReadMemStats(&mem)

	snap := statusSnapshot{
		Version:    currentVersion(),
		GoVersion:  runtime.Version(),
		Discordgo:  discordgo.VERSION,
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		Started:    startTime,
		Uptime:     time.Since(startTime),
		Goroutines: runtime.NumGoroutine(),
		HeapAlloc:  mem.HeapAlloc,
		HeapSys:    mem.HeapSys,
		Sys:        mem.Sys,
		StackInuse: mem.StackInuse,
		NumGC:      mem.NumGC,
	}
	if mem.LastGC > 0 {
		snap.LastGC = time.Unix(0, int64(mem.LastGC))
	}

	if s != nil {
		s.RLock()
		snap.VoiceConns = len(s.VoiceConnections)
		if !s.LastHeartbeatAck.IsZero() && s.LastHeartbeatAck.After(s.LastHeartbeatSent) {
			snap.Latency = s.LastHeartbeatAck.Sub(s.LastHeartbeatSent)
		}
		voiceGuilds := make(map[string]bool, len(s.VoiceConnections))
		for id := range s.VoiceConnections {
			voiceGuilds[id] = true
		}
		s.RUnlock()

		if s.State != nil {
			s.State.RLock()
			snap.Guilds = len(s.State.Guilds)
			for _, g := range s.State.Guilds {
				name := g.Name
				if name == "" {
					name = g.ID
				}
				snap.GuildList = append(snap.GuildList, guildStatus{Name: name, Members: g.MemberCount, Voice: voiceGuilds[g.ID]})
			}
			s.State.RUnlock()
			sort.Slice(snap.GuildList, func(i, j int) bool {
				if snap.GuildList[i].Members != snap.GuildList[j].Members {
					return snap.GuildList[i].Members > snap.GuildList[j].Members
				}
				return snap.GuildList[i].Name < snap.GuildList[j].Name
			})
		}
	}

	snap.DBFiles = collectDBFiles(dbPaths)
	snap.Modules = collectModuleStats(ctx, providers, statusProviderTimeout)
	return snap
}

func collectDBFiles(paths []string) []dbFileStatus {
	seen := make(map[string]bool, len(paths))
	out := make([]dbFileStatus, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		st := dbFileStatus{Path: p}
		if fi, err := os.Stat(p); err != nil {
			st.Err = err
		} else {
			st.Size = fi.Size()
			// WAL contents are part of the live database size.
			if wal, err := os.Stat(p + "-wal"); err == nil {
				st.Size += wal.Size()
			}
		}
		out = append(out, st)
	}
	return out
}

// collectModuleStats queries providers concurrently. A provider that does not
// return within timeout is reported as timed out; its result is discarded.
func collectModuleStats(ctx context.Context, providers []bot.StatsProvider, timeout time.Duration) []moduleStatus {
	out := make([]moduleStatus, len(providers))
	var wg sync.WaitGroup
	for idx, p := range providers {
		out[idx].Name = p.Name()
		wg.Add(1)
		go func(idx int, p bot.StatsProvider) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			type result struct {
				stats bot.ModuleStats
				err   error
			}
			done := make(chan result, 1)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						done <- result{err: fmt.Errorf("panic: %v", r)}
					}
				}()
				st, err := p.Stats(pctx)
				done <- result{st, err}
			}()

			select {
			case r := <-done:
				out[idx].Stats, out[idx].Err = r.stats, r.err
			case <-pctx.Done():
				out[idx].Err = fmt.Errorf("timed out after %s", timeout)
			}
			if errors.Is(out[idx].Err, context.DeadlineExceeded) {
				out[idx].Err = fmt.Errorf("timed out after %s", timeout)
			}
		}(idx, p)
	}
	wg.Wait()
	return out
}

func soundboardStatsProvider() bot.StatsProvider {
	return bot.StatsFunc{ModuleName: "soundboard", Fn: func(context.Context) (bot.ModuleStats, error) {
		sounds := 0
		for _, c := range COLLECTIONS {
			sounds += len(c.Sounds)
		}
		mutex.Lock()
		queued, active := 0, len(queues)
		for _, q := range queues {
			queued += len(q)
		}
		playing := len(nowPlaying)
		mutex.Unlock()

		return bot.ModuleStats{
			Summary: []bot.Stat{
				{Name: "sounds", Value: strconv.Itoa(sounds)},
				{Name: "collections", Value: strconv.Itoa(len(COLLECTIONS))},
				{Name: "queued", Value: strconv.Itoa(queued)},
			},
			Detail: []bot.Stat{
				{Name: "playing", Value: strconv.Itoa(playing)},
				{Name: "active queues", Value: strconv.Itoa(active)},
				{Name: "max queue", Value: strconv.Itoa(maxQueueSize)},
			},
		}, nil
	}}
}

// ---- rendering ----

func formatUptime(d time.Duration) string {
	d = d.Round(time.Minute)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func formatLatency(d time.Duration) string {
	if d <= 0 {
		return "n/a"
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + " ms"
}

func joinStats(stats []bot.Stat) string {
	parts := make([]string, 0, len(stats))
	for _, st := range stats {
		parts = append(parts, st.Value+" "+st.Name)
	}
	return strings.Join(parts, " · ")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func (snap statusSnapshot) headerLines() []string {
	return []string{
		fmt.Sprintf("gidbig %s · %s %s", snap.Version, snap.GoVersion, snap.Platform),
		fmt.Sprintf("up %s · since %s", formatUptime(snap.Uptime), snap.Started.UTC().Format("2006-01-02 15:04 UTC")),
	}
}

func (snap statusSnapshot) warnings() []string {
	var out []string
	for _, m := range snap.Modules {
		if m.Err != nil {
			out = append(out, truncateRunes(m.Name+": "+m.Err.Error(), statusWarningMaxRunes))
		}
	}
	for _, f := range snap.DBFiles {
		if f.Err != nil {
			out = append(out, truncateRunes(filepath.Base(f.Path)+": "+f.Err.Error(), statusWarningMaxRunes))
		}
	}
	return out
}

func pair(k1, v1, k2, v2 string) string {
	return fmt.Sprintf("%-11s %-10s %-12s %s", k1, v1, k2, v2)
}

func (snap statusSnapshot) runtimeLines(detailed bool) []string {
	lines := []string{
		pair("guilds", strconv.Itoa(snap.Guilds), "goroutines", strconv.Itoa(snap.Goroutines)),
		pair("heap", humanize.IBytes(snap.HeapAlloc), "gc cycles", humanize.Comma(int64(snap.NumGC))),
		pair("gateway", formatLatency(snap.Latency), "voice conns", strconv.Itoa(snap.VoiceConns)),
	}
	if detailed {
		lastGC := "never"
		if !snap.LastGC.IsZero() {
			lastGC = humanize.Time(snap.LastGC)
		}
		lines = append(lines,
			pair("sys mem", humanize.IBytes(snap.Sys), "heap sys", humanize.IBytes(snap.HeapSys)),
			pair("stack", humanize.IBytes(snap.StackInuse), "last gc", lastGC),
			fmt.Sprintf("%-11s %s", "discordgo", snap.Discordgo),
		)
	}
	return lines
}

func (snap statusSnapshot) dbLines() []string {
	lines := make([]string, 0, len(snap.DBFiles))
	for _, f := range snap.DBFiles {
		size := "n/a"
		if f.Err == nil {
			size = humanize.IBytes(uint64(f.Size))
		}
		lines = append(lines, fmt.Sprintf("%-11s %s", filepath.Base(f.Path), size))
	}
	return lines
}

func (snap statusSnapshot) moduleLines(detailed bool) []string {
	var lines []string
	for _, m := range snap.Modules {
		if m.Err != nil {
			lines = append(lines, fmt.Sprintf("%-11s n/a", m.Name))
			continue
		}
		lines = append(lines, fmt.Sprintf("%-11s %s", m.Name, joinStats(m.Stats.Summary)))
		if detailed && len(m.Stats.Detail) > 0 {
			lines = append(lines, fmt.Sprintf("%-11s %s", "", joinStats(m.Stats.Detail)))
		}
	}
	return lines
}

func (snap statusSnapshot) guildLines() []string {
	lines := make([]string, 0, len(snap.GuildList))
	for _, g := range snap.GuildList {
		voice := ""
		if g.Voice {
			voice = " · voice"
		}
		lines = append(lines, fmt.Sprintf("%-24s %6s members%s", truncateRunes(g.Name, 24), humanize.Comma(int64(g.Members)), voice))
	}
	return lines
}

type textSection struct {
	title     string
	lines     []string
	trimmable bool
	omitted   int
}

func rule(title string) string {
	prefix := "── " + title + " "
	n := statusRuleWidth - len([]rune(prefix))
	if n < 3 {
		n = 3
	}
	return prefix + strings.Repeat("─", n)
}

func renderSections(header []string, sections []textSection) string {
	var b strings.Builder
	b.WriteString(strings.Join(header, "\n"))
	for _, sec := range sections {
		if len(sec.lines) == 0 && sec.omitted == 0 {
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(rule(sec.title))
		for _, l := range sec.lines {
			b.WriteString("\n")
			b.WriteString(l)
		}
		if sec.omitted > 0 {
			fmt.Fprintf(&b, "\n… +%d more", sec.omitted)
		}
	}
	return b.String()
}

// renderStatusText renders the monospace layout, trimming the last trimmable
// section line by line until the result fits in limit runes.
func renderStatusText(snap statusSnapshot, view string, limit int) string {
	detailed := view == statusViewDetailed
	sections := []textSection{
		{title: "Runtime", lines: snap.runtimeLines(detailed)},
		{title: "Database", lines: snap.dbLines()},
		{title: "Modules", lines: snap.moduleLines(detailed), trimmable: true},
	}
	if detailed {
		sections = append(sections, textSection{title: fmt.Sprintf("Guilds (%d)", len(snap.GuildList)), lines: snap.guildLines(), trimmable: true})
	}
	if w := snap.warnings(); len(w) > 0 {
		for i := range w {
			w[i] = "! " + w[i]
		}
		sections = append(sections, textSection{title: "Warnings", lines: w, trimmable: true})
	}

	header := snap.headerLines()
	for {
		out := renderSections(header, sections)
		if len([]rune(out)) <= limit {
			return out
		}
		trimmed := false
		for i := len(sections) - 1; i >= 0; i-- {
			if sections[i].trimmable && len(sections[i].lines) > 0 {
				sections[i].lines = sections[i].lines[:len(sections[i].lines)-1]
				sections[i].omitted++
				trimmed = true
				break
			}
		}
		if !trimmed {
			return truncateRunes(out, limit)
		}
	}
}

func statusTextResponseData(snap statusSnapshot, view string) *discordgo.InteractionResponseData {
	const fence = "```"
	body := renderStatusText(snap, view, discordMessageLimit-2*len(fence))
	return &discordgo.InteractionResponseData{
		Content: fence + body + fence,
		Flags:   discordgo.MessageFlagsEphemeral,
	}
}

func embedValue(lines []string) string {
	if len(lines) == 0 {
		return "—"
	}
	const fence = "```"
	limit := embedFieldValueLimit - 2*len(fence) - len("\n… +9999 more")
	var kept []string
	size := 0
	for _, l := range lines {
		n := len([]rune(l)) + 1
		if size+n > limit {
			break
		}
		kept = append(kept, l)
		size += n
	}
	body := strings.Join(kept, "\n")
	if omitted := len(lines) - len(kept); omitted > 0 {
		body += fmt.Sprintf("\n… +%d more", omitted)
	}
	return fence + body + fence
}

func statusEmbedResponseData(snap statusSnapshot, view string) *discordgo.InteractionResponseData {
	detailed := view == statusViewDetailed
	warnings := snap.warnings()
	color := statusEmbedColorOK
	if len(warnings) > 0 {
		color = statusEmbedColorWarn
	}

	fields := []*discordgo.MessageEmbedField{
		{Name: "Runtime", Value: embedValue(snap.runtimeLines(detailed))},
		{Name: "Database", Value: embedValue(snap.dbLines())},
	}
	for _, m := range snap.Modules {
		var lines []string
		if m.Err != nil {
			lines = []string{"n/a"}
		} else {
			for _, st := range m.Stats.Summary {
				lines = append(lines, fmt.Sprintf("%-12s %s", st.Name, st.Value))
			}
			if detailed {
				for _, st := range m.Stats.Detail {
					lines = append(lines, fmt.Sprintf("%-12s %s", st.Name, st.Value))
				}
			}
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: m.Name, Value: embedValue(lines), Inline: true})
	}
	if detailed {
		fields = append(fields, &discordgo.MessageEmbedField{Name: fmt.Sprintf("Guilds (%d)", len(snap.GuildList)), Value: embedValue(snap.guildLines())})
	}
	if len(warnings) > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Warnings", Value: embedValue(warnings)})
	}
	if len(fields) > 25 {
		fields = fields[:25]
	}

	footer := "summary · /status view:detailed for more"
	if detailed {
		footer = "detailed"
	}
	header := snap.headerLines()
	return &discordgo.InteractionResponseData{
		Embeds: []*discordgo.MessageEmbed{{
			Title:       "gidbig status",
			Description: header[0] + "\n" + header[1],
			Color:       color,
			Fields:      fields,
			Footer:      &discordgo.MessageEmbedFooter{Text: footer},
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		}},
		Flags: discordgo.MessageFlagsEphemeral,
	}
}

func statusResponseData(snap statusSnapshot, view, format string) *discordgo.InteractionResponseData {
	if format == statusFormatEmbed {
		return statusEmbedResponseData(snap, view)
	}
	return statusTextResponseData(snap, view)
}

// buildBotStatsMessage returns the compact text status (used by /admin info).
func buildBotStatsMessage(s *discordgo.Session) string {
	snap := collectStatus(context.Background(), s, statusProviders, statusDBPaths)
	return renderStatusText(snap, statusViewSummary, discordMessageLimit-6)
}
