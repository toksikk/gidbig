package react

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// sessionTTL matches Discord's 15-minute interaction token lifetime.
const sessionTTL = 15 * time.Minute

// pickerSession is the state of one ephemeral emoji picker.
type pickerSession struct {
	invokerID string
	channelID string
	messageID string
	guildID   string
	filter    string
	emojis    []*discordgo.Emoji
	expires   time.Time
}

func (p *pickerSession) emojiByID(id string) *discordgo.Emoji {
	for _, e := range p.emojis {
		if e.ID == id {
			return e
		}
	}
	return nil
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]*pickerSession
	now      func() time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]*pickerSession), now: time.Now}
}

func newToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("react: new token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// put stores p under a fresh token and returns it.
func (s *sessionStore) put(p *pickerSession) (string, error) {
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p.expires = s.now().Add(sessionTTL)
	s.sessions[tok] = p
	return tok, nil
}

// get returns the live session for tok, dropping it when expired.
func (s *sessionStore) get(tok string) (*pickerSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.sessions[tok]
	if !ok {
		return nil, false
	}
	if !s.now().Before(p.expires) {
		delete(s.sessions, tok)
		return nil, false
	}
	return p, true
}

func (s *sessionStore) remove(tok string) {
	s.mu.Lock()
	delete(s.sessions, tok)
	s.mu.Unlock()
}

// sweep drops every session expired at now.
func (s *sessionStore) sweep(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, p := range s.sessions {
		if !now.Before(p.expires) {
			delete(s.sessions, tok)
		}
	}
}

func (s *sessionStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}
