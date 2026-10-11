package util

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/bwmarrin/discordgo"
)

// maxResolveCandidates caps how many ambiguous matches are listed back to the
// caller so one lookup cannot flood the model context.
const maxResolveCandidates = 10

// MemberMatch is a guild member resolved from a free-text query.
type MemberMatch struct {
	ID          string
	Username    string // Discord handle
	DisplayName string // global display name, falling back to the handle
	Nick        string // per-guild nickname
}

// Label returns the most specific human-readable name for the member.
func (m MemberMatch) Label() string {
	switch {
	case m.Nick != "":
		return m.Nick
	case m.DisplayName != "":
		return m.DisplayName
	case m.Username != "":
		return m.Username
	}
	return m.ID
}

// IsUserID reports whether s looks like a Discord snowflake: at least 15 ASCII
// digits. Used to pass real IDs straight through without a member lookup.
func IsUserID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 15 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// mentionID extracts the ID from a <@id> or <@!id> mention, or "" when s is not
// a mention.
func mentionID(s string) string {
	id, ok := strings.CutPrefix(s, "<@")
	if !ok {
		return ""
	}
	id = strings.TrimPrefix(id, "!")
	id, ok = strings.CutSuffix(id, ">")
	if !ok {
		return ""
	}
	return id
}

// ResolveMemberQuery resolves a user-supplied query to exactly one guild member.
// The query may be an ID, a mention, a username, a nickname, a display name, or
// a partial/leetspeak form of any of those.
//
// It returns the member and ok=true when exactly one member matches. When
// nothing matches, or several members match, it returns ok=false together with
// a message the caller can hand back to the model (a candidate list for
// ambiguity). Resolution is scoped to guildID, so global record lookups can
// still only name people who share the caller's server.
func ResolveMemberQuery(session *discordgo.Session, guildID, query string) (MemberMatch, bool, string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return MemberMatch{}, false, "no user given"
	}
	if mention := mentionID(query); mention != "" {
		query = mention
	}
	if IsUserID(query) {
		return MemberMatch{ID: query}, true, ""
	}
	if guildID == "" {
		return MemberMatch{}, false, "no guild available"
	}
	matches, err := ResolveMember(session, guildID, query)
	if err != nil {
		return MemberMatch{}, false, fmt.Sprintf("could not look up members in this server: %v", err)
	}
	switch len(matches) {
	case 0:
		return MemberMatch{}, false, fmt.Sprintf("no member matching %q in this server; ask the user for the exact name or ID", query)
	case 1:
		return matches[0], true, ""
	}
	labels := make([]string, 0, len(matches))
	for i, match := range matches {
		if i == maxResolveCandidates {
			break
		}
		labels = append(labels, fmt.Sprintf("%s (%s)", match.Label(), match.ID))
	}
	return MemberMatch{}, false, fmt.Sprintf("%q matches several members: %s; ask the user which one", query, strings.Join(labels, ", "))
}

// ResolveMember returns the guild members whose username, nickname or display
// name matches query, best match first. Matching is case-insensitive and
// tolerant of separators and common leetspeak substitutions. The member list
// comes from the session state and the List Guild Members REST endpoint (which
// requires the privileged Server Members Intent); when REST fails it falls back
// to whatever the state cache already holds.
func ResolveMember(session *discordgo.Session, guildID, query string) ([]MemberMatch, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	members, err := guildMembers(session, guildID)
	if err != nil {
		return nil, err
	}
	return rankMembers(members, query), nil
}

// guildMembers merges the guild's cached state members with the REST member
// list. A REST failure is only fatal when the state cache also has nothing.
func guildMembers(session *discordgo.Session, guildID string) ([]*discordgo.Member, error) {
	if session == nil {
		return nil, errors.New("no session")
	}
	byID := make(map[string]*discordgo.Member)
	if session.State != nil {
		if guild, err := session.State.Guild(guildID); err == nil && guild != nil {
			for _, member := range guild.Members {
				if member != nil && member.User != nil {
					byID[member.User.ID] = member
				}
			}
		}
	}
	restMembers, restErr := listGuildMembers(session, guildID)
	for _, member := range restMembers {
		if member != nil && member.User != nil {
			byID[member.User.ID] = member
		}
	}
	if len(byID) == 0 && restErr != nil {
		return nil, restErr
	}
	members := make([]*discordgo.Member, 0, len(byID))
	for _, member := range byID {
		members = append(members, member)
	}
	return members, nil
}

// Match ranks, lowest first. A lower rank is a better match.
const (
	rankExact = iota
	rankNormalized
	rankPrefix
	rankContains
	rankNone
)

// rankMembers scores every member against query and returns the matching ones,
// best rank first and then alphabetically by label. Members are already unique
// by ID because guildMembers dedupes them.
func rankMembers(members []*discordgo.Member, query string) []MemberMatch {
	type scoredMatch struct {
		match MemberMatch
		rank  int
	}
	scored := make([]scoredMatch, 0, len(members))
	for _, member := range members {
		if member == nil || member.User == nil {
			continue
		}
		match := MemberMatch{
			ID:          member.User.ID,
			Username:    member.User.Username,
			DisplayName: member.User.DisplayName(),
			Nick:        member.Nick,
		}
		rank := rankNone
		for _, field := range []string{match.Username, match.Nick, match.DisplayName} {
			if fieldRank := matchRank(query, field); fieldRank < rank {
				rank = fieldRank
			}
		}
		if rank == rankNone {
			continue
		}
		scored = append(scored, scoredMatch{match: match, rank: rank})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].rank != scored[j].rank {
			return scored[i].rank < scored[j].rank
		}
		return strings.ToLower(scored[i].match.Label()) < strings.ToLower(scored[j].match.Label())
	})
	out := make([]MemberMatch, 0, len(scored))
	for _, entry := range scored {
		out = append(out, entry.match)
	}
	return out
}

// matchRank compares one candidate field against the query. Exact
// case-insensitive matches beat normalized matches, which beat prefixes, which
// beat substrings. Very short queries are not matched as prefixes/substrings so
// a single letter does not return the whole guild.
func matchRank(query, field string) int {
	field = strings.TrimSpace(field)
	if field == "" {
		return rankNone
	}
	if strings.EqualFold(strings.TrimSpace(query), field) {
		return rankExact
	}
	normalizedQuery, normalizedField := normalizeName(query), normalizeName(field)
	if normalizedQuery == "" || normalizedField == "" {
		return rankNone
	}
	switch {
	case normalizedQuery == normalizedField:
		return rankNormalized
	case len(normalizedQuery) >= 2 && strings.HasPrefix(normalizedField, normalizedQuery):
		return rankPrefix
	case len(normalizedField) >= 2 && strings.HasPrefix(normalizedQuery, normalizedField):
		return rankPrefix
	case len(normalizedQuery) >= 3 && strings.Contains(normalizedField, normalizedQuery):
		return rankContains
	case len(normalizedField) >= 3 && strings.Contains(normalizedQuery, normalizedField):
		return rankContains
	}
	return rankNone
}

// leetFold maps common leetspeak substitutions onto letters so "s1kk" and
// "sikk" normalize to the same value.
var leetFold = strings.NewReplacer(
	"0", "o", "1", "i", "3", "e", "4", "a", "5", "s",
	"7", "t", "8", "b", "9", "g", "@", "a", "$", "s", "!", "i", "|", "i",
)

// normalizeName lowercases a name, folds leetspeak and drops every separator,
// so "s1kk", "Sikk" and "s_i-k.k" all compare equal.
func normalizeName(s string) string {
	s = leetFold.Replace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
