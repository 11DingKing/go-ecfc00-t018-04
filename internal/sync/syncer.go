// Package sync runs background reconciliation for the patrol dispatch service:
// it flags incidents that breach the 30-minute response SLA and replays
// offline-cached records once connectivity is restored. A toggleable network
// state models signal loss so the failure-recovery path is exercised without a
// real radio link.
package sync

import (
	"context"
	stdsync "sync"
	"sync/atomic"
	"time"

	"qilian-patrol/internal/app"
	"qilian-patrol/internal/store"
)

// Syncer drives the SLA monitor and offline reconciliation loops.
type Syncer struct {
	svc         *app.Service
	store       *store.Store
	interval    time.Duration
	online      atomic.Bool
	runMu       stdsync.Mutex // guards the last-run timestamps below
	slaLastRun  time.Time
	syncLastRun time.Time
	syncedCount atomic.Int64
	failedCount atomic.Int64
	slaFlagged  atomic.Int64
}

// New constructs a Syncer.
func New(svc *app.Service, interval time.Duration) *Syncer {
	sy := &Syncer{
		svc:      svc,
		store:    svc.FromStore(),
		interval: interval,
	}
	sy.online.Store(true)
	return sy
}

// SetOnline toggles simulated connectivity. When offline the syncer keeps
// collecting pending records but stops replaying them.
func (s *Syncer) SetOnline(v bool) { s.online.Store(v) }

// IsOnline reports the current connectivity state.
func (s *Syncer) IsOnline() bool { return s.online.Load() }

// SyncedCount returns the total number of records reconciled.
func (s *Syncer) SyncedCount() int64 { return s.syncedCount.Load() }

// FailedCount returns the total number of failed sync attempts.
func (s *Syncer) FailedCount() int64 { return s.failedCount.Load() }

// SLAFlagged returns the total number of incidents flagged for SLA breach.
func (s *Syncer) SLAFlagged() int64 { return s.slaFlagged.Load() }

// Run drives both loops until ctx is cancelled. It is safe to call in its own
// goroutine; tests typically call TickOnce directly for determinism.
func (s *Syncer) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.TickOnce()
		}
	}
}

// TickOnce runs one SLA-monitor pass and, when online, one offline-reconcile
// pass. Returns the counts touched in this tick.
func (s *Syncer) TickOnce() (slaFlagged, synced, failed int) {
	slaFlagged = s.monitorSLA()
	synced, failed = s.reconcileOffline()
	return
}

// monitorSLA flags reported incidents that have exceeded the response SLA.
func (s *Syncer) monitorSLA() int {
	n := s.svc.FlagSLABreaches()
	s.runMu.Lock()
	s.slaLastRun = s.store.Clock().Now()
	s.runMu.Unlock()
	s.slaFlagged.Add(int64(n))
	return n
}

// reconcileOffline replays pending offline records when connectivity is
// available. Each record is applied exactly once thanks to the idempotency of
// the underlying operations; a second replay of the same record is a no-op and
// still produces a receipt.
func (s *Syncer) reconcileOffline() (synced, failed int) {
	if !s.online.Load() {
		return 0, 0
	}
	s.runMu.Lock()
	s.syncLastRun = s.store.Clock().Now()
	s.runMu.Unlock()
	for _, r := range s.store.PendingOffline() {
		rec, ok := s.store.MarkSyncing(r.LocalID)
		if !ok {
			continue
		}
		receipt, err := s.svc.ApplyOfflineRecord(rec)
		if err != nil {
			s.store.MarkFailed(r.LocalID, err.Error())
			s.failedCount.Add(1)
			failed++
			continue
		}
		s.store.MarkSynced(r.LocalID, receipt)
		s.syncedCount.Add(1)
		synced++
	}
	return synced, failed
}
