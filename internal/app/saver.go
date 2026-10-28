package app

import (
	"sync"

	"intra/internal/config"
)

// saver writes configuration snapshots in the background so disk I/O never
// blocks the UI thread. Only the newest pending snapshot is written.
type saver struct {
	store   *config.Store
	onError func(error)

	mu      sync.Mutex
	cond    *sync.Cond
	pending *config.Config
	seq     uint64 // seq of pending
	done    uint64 // highest seq written
	busy    bool
}

func newSaver(store *config.Store, onError func(error)) *saver {
	s := &saver{store: store, onError: onError}
	s.cond = sync.NewCond(&s.mu)
	go s.loop()
	return s
}

func (s *saver) save(c *config.Config) {
	s.mu.Lock()
	s.seq++
	s.pending = c
	s.mu.Unlock()
	s.cond.Broadcast()
}

func (s *saver) loop() {
	for {
		s.mu.Lock()
		for s.pending == nil {
			s.cond.Wait()
		}
		c, seq := s.pending, s.seq
		s.pending = nil
		s.busy = true
		s.mu.Unlock()

		err := s.store.Save(c)

		s.mu.Lock()
		s.busy = false
		if seq > s.done {
			s.done = seq
		}
		s.mu.Unlock()
		s.cond.Broadcast()
		if err != nil && s.onError != nil {
			s.onError(err)
		}
	}
}

// flush writes c synchronously after any in-flight save, used at exit.
func (s *saver) flush(c *config.Config) {
	s.mu.Lock()
	s.pending = nil
	for s.busy {
		s.cond.Wait()
	}
	s.seq++
	s.done = s.seq
	s.busy = true // keep the background loop out while we write
	s.mu.Unlock()
	s.store.Save(c)
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}
