package subscribers

import "sync"

// Store keeps subscriptions in memory for the initial scaffold. Replace it with
// a durable implementation before running more than one bot instance.
type Store struct {
	mu      sync.RWMutex
	chatIDs map[int64]struct{}
}

func NewStore() *Store {
	return &Store{chatIDs: make(map[int64]struct{})}
}

func (s *Store) Add(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatIDs[chatID] = struct{}{}
}

func (s *Store) Remove(chatID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.chatIDs, chatID)
}

func (s *Store) Contains(chatID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.chatIDs[chatID]
	return ok
}

func (s *Store) All() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]int64, 0, len(s.chatIDs))
	for id := range s.chatIDs {
		ids = append(ids, id)
	}
	return ids
}
