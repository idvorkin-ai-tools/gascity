package beads

import (
	"time"
)

// PurgeExpired removes ephemeral beads whose expires_at metadata is in the
// past. It returns the number of beads removed.
func (s *HQStore) PurgeExpired() (int, error) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpenLocked(); err != nil {
		return 0, err
	}

	var ids []string
	for id, bead := range s.wisps {
		expiresAt, ok := hqBeadExpiresAt(bead)
		if ok && expiresAt.Before(now) {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		s.deleteLocked(id)
	}
	return len(ids), nil
}

func hqBeadExpiresAt(b Bead) (time.Time, bool) {
	if len(b.Metadata) == 0 {
		return time.Time{}, false
	}
	raw := b.Metadata[hqExpiresAtMetadataKey]
	if raw == "" {
		raw = b.Metadata[hqExpiresAtMetadataAlt]
	}
	if raw == "" {
		return time.Time{}, false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return expiresAt, true
}

func (s *HQStore) startTTLSweeper() {
	if s.ttlInterval <= 0 {
		return
	}
	s.ttlStop = make(chan struct{})
	s.ttlDone = make(chan struct{})
	go func() {
		defer close(s.ttlDone)
		ticker := time.NewTicker(s.ttlInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _ = s.PurgeExpired()
			case <-s.ttlStop:
				return
			}
		}
	}()
}

func (s *HQStore) stopTTLSweeper() {
	if s.ttlStop == nil {
		return
	}
	close(s.ttlStop)
	<-s.ttlDone
	s.ttlStop = nil
	s.ttlDone = nil
}
