package trivia

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// dropinTimeout bounds one drop-in: leader queries, LLM call and send.
const dropinTimeout = 45 * time.Second

// dropinJob asks the worker to post one drop-in. prev is the cooldown value
// before this job claimed it, restored when no drop-in is posted.
type dropinJob struct {
	guildID   string
	channelID string
	claimed   time.Time
	prev      time.Time
}

// statLabels describe each stat for the LLM prompt.
var statLabels = map[string]string{
	statMessages:          "messages sent",
	statWords:             "words written",
	statGIFs:              "GIFs posted",
	statPictures:          "pictures posted",
	statLinks:             "links posted",
	statReactionsGiven:    "reactions given",
	statReactionsReceived: "reactions received",
}

const dropinInstruction = "Write exactly one short, playful trivia sentence for this Discord chat, " +
	"in the chat's language. Use the given name and number exactly; do not change them, " +
	"do not add an @ mention, and do not add other facts."

// maybeTrigger rolls the drop-in chance and, outside the guild cooldown,
// claims the cooldown and queues a job. It never blocks on the worker.
func (m *Module) maybeTrigger(ctx context.Context, guildID, channelID string) {
	if m.chance <= 0 || m.generate == nil || m.send == nil {
		return
	}
	if m.rand() >= m.chance {
		return
	}
	if err := m.loadCooldown(ctx, guildID); err != nil {
		slog.Warn("trivia: load cooldown", "guild", guildID, "error", err)
		return
	}
	now := m.now()
	m.mu.Lock()
	prev := m.lastDropin[guildID]
	if !prev.IsZero() && now.Sub(prev) < m.cooldown {
		m.mu.Unlock()
		return
	}
	m.lastDropin[guildID] = now
	m.mu.Unlock()

	job := dropinJob{guildID: guildID, channelID: channelID, claimed: now, prev: prev}
	// Persist before queueing so a fast release in the worker cannot be
	// overwritten by this write.
	if err := m.store.setLastDropin(ctx, guildID, now); err != nil {
		slog.Warn("trivia: save cooldown", "guild", guildID, "error", err)
	}
	select {
	case m.jobs <- job:
	default:
		// The worker is busy with another guild's drop-in; give the claim back.
		m.release(ctx, job)
	}
}

// loadCooldown reads the persisted cooldown for guildID once per process.
func (m *Module) loadCooldown(ctx context.Context, guildID string) error {
	m.mu.Lock()
	_, ok := m.lastDropin[guildID]
	m.mu.Unlock()
	if ok {
		return nil
	}
	last, err := m.store.lastDropin(ctx, guildID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if _, ok := m.lastDropin[guildID]; !ok {
		m.lastDropin[guildID] = last
	}
	m.mu.Unlock()
	return nil
}

// release restores the cooldown a job claimed, unless a newer claim exists.
func (m *Module) release(ctx context.Context, job dropinJob) {
	m.mu.Lock()
	if !m.lastDropin[job.guildID].Equal(job.claimed) {
		m.mu.Unlock()
		return
	}
	m.lastDropin[job.guildID] = job.prev
	m.mu.Unlock()
	if err := m.store.setLastDropin(ctx, job.guildID, job.prev); err != nil {
		slog.Warn("trivia: restore cooldown", "guild", job.guildID, "error", err)
	}
}

func (m *Module) runDropinWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-m.jobs:
			jobCtx, cancel := context.WithTimeout(ctx, dropinTimeout)
			if err := m.postDropin(jobCtx, job); err != nil {
				m.dropinsSkipped.Add(1)
				slog.Warn("trivia: drop-in skipped", "guild", job.guildID, "error", err)
				m.release(context.WithoutCancel(ctx), job)
			} else {
				m.dropinsPosted.Add(1)
			}
			cancel()
		}
	}
}

// fact is one drop-in subject.
type fact struct {
	stat   string
	window string // "all time" or "the last 30 days"
	name   string
	total  int64
}

// pickFact walks the stat/window combinations in random order and returns the
// first one with enough consenting participants.
func (m *Module) pickFact(ctx context.Context, guildID string) (fact, bool, error) {
	type combo struct{ stat, since, window string }
	since := dayOf(m.now().AddDate(0, 0, -(windowDays - 1)))
	combos := make([]combo, 0, len(allStats)*2)
	for _, s := range allStats {
		combos = append(combos, combo{s, "", "all time"}, combo{s, since, fmt.Sprintf("the last %d days", windowDays)})
	}
	m.shuffle(len(combos), func(i, j int) { combos[i], combos[j] = combos[j], combos[i] })
	for _, c := range combos {
		l, ok, err := m.store.leaderFor(ctx, guildID, c.stat, c.since, m.counted)
		if err != nil {
			return fact{}, false, err
		}
		if !ok {
			continue
		}
		name := m.name(guildID, l.UserID)
		if name == "" {
			continue
		}
		return fact{stat: c.stat, window: c.window, name: name, total: l.Total}, true, nil
	}
	return fact{}, false, nil
}

func (m *Module) postDropin(ctx context.Context, job dropinJob) error {
	f, ok, err := m.pickFact(ctx, job.guildID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no stat has %d consenting participants", minParticipants)
	}
	system := dropinInstruction
	if m.persona != nil {
		if p := strings.TrimSpace(m.persona()); p != "" {
			system = p + "\n\n" + dropinInstruction
		}
	}
	user := fmt.Sprintf("Fact: %s leads the server in %s over %s with %d.", f.name, statLabels[f.stat], f.window, f.total)
	text, err := m.generate(ctx, system, user)
	if err != nil {
		return fmt.Errorf("generate drop-in: %w", err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("generate drop-in: empty reply")
	}
	msg := &discordgo.MessageSend{
		Content:         text,
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}},
	}
	if err := m.send(job.channelID, msg); err != nil {
		return fmt.Errorf("send drop-in: %w", err)
	}
	slog.Info("trivia: drop-in posted", "guild", job.guildID, "stat", f.stat, "window", f.window)
	return nil
}
