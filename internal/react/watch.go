package react

import (
	"container/list"
	"sync"
)

// maxWatches caps the in-memory watch store; the oldest entry is evicted first.
const maxWatches = 1000

type watchKey struct {
	messageID string
	emojiID   string
	userID    string
}

// watchStore remembers which (message, emoji, invoker) reactions the bot
// removes. It is in-memory only and lost on restart.
type watchStore struct {
	mu    sync.Mutex
	max   int
	order *list.List
	index map[watchKey]*list.Element
}

func newWatchStore(max int) *watchStore {
	return &watchStore{max: max, order: list.New(), index: make(map[watchKey]*list.Element)}
}

// add records k. Re-adding an existing key moves it to the newest position.
func (w *watchStore) add(k watchKey) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if el, ok := w.index[k]; ok {
		w.order.MoveToBack(el)
		return
	}
	w.index[k] = w.order.PushBack(k)
	for w.order.Len() > w.max {
		oldest := w.order.Front()
		w.order.Remove(oldest)
		delete(w.index, oldest.Value.(watchKey))
	}
}

func (w *watchStore) has(k watchKey) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.index[k]
	return ok
}

func (w *watchStore) len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.order.Len()
}
