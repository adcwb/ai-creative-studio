package uploadrepo

import (
	"context"
	"log/slog"
	"time"
)

// Maintenance removes bytes only from expired temporary sessions. It does not
// remove completed media, projects or receipt identities. Each batch is bounded.
func (s *Store) StartMaintenance() {
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				for {
					n, e := s.Expire(ctx, now, 100)
					if e != nil {
						if ctx.Err() == nil {
							slog.Warn("temporary upload cleanup failed; retrying next maintenance cycle")
						}
						break
					}
					if n < 100 {
						break
					}
				}
			}
		}
	}()
}
func (s *Store) Close() {
	s.maintenanceMu.Lock()
	cancel, done := s.cancel, s.done
	s.maintenanceMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
