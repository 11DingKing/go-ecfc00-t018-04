package store

import (
	"time"
)

// OfflineStatus is the sync lifecycle of an offline-cached record.
type OfflineStatus string

const (
	// OfflinePending: stored locally, awaiting connectivity.
	OfflinePending OfflineStatus = "pending"
	// OfflineSyncing: a sync worker has picked it up.
	OfflineSyncing OfflineStatus = "syncing"
	// OfflineSynced: confirmed applied server-side with a receipt.
	OfflineSynced OfflineStatus = "synced"
	// OfflineFailed: exhausted retries; awaits manual retry.
	OfflineFailed OfflineStatus = "failed"
)

// OfflineKind classifies the payload type of an offline record.
type OfflineKind string

const (
	OfflineKindIncident OfflineKind = "incident"
	OfflineKindTrack    OfflineKind = "track"
	OfflineKindPatrol   OfflineKind = "patrol"
)

// OfflineRecord is a patrol record or event cached on a device without signal.
// Each record carries a client-generated LocalID so re-submission is
// idempotent: applying the same LocalID twice yields one server effect and one
// receipt.
type OfflineRecord struct {
	LocalID   string        `json:"localId"`
	Kind      OfflineKind   `json:"kind"`
	Payload   []byte        `json:"payload"`
	Status    OfflineStatus `json:"status"`
	Attempts  int           `json:"attempts"`
	LastError string        `json:"lastError"`
	CreatedAt time.Time     `json:"createdAt"`
	SyncedAt  time.Time     `json:"syncedAt"`
	ReceiptID string        `json:"receiptId"`
}

// OfflineStore operations are exposed as methods on Store so the offline cache
// shares the same lock domain as the aggregates it reconciles against.

// EnqueueOffline stores a new pending offline record. If a record with the same
// LocalID already exists it is returned unchanged (idempotent enqueue).
func (s *Store) EnqueueOffline(r OfflineRecord) (OfflineRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.offline[r.LocalID]; ok {
		return existing, false
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = s.clock.Now()
	}
	if r.Status == "" {
		r.Status = OfflinePending
	}
	s.offline[r.LocalID] = r
	return r, true
}

// PendingOffline returns records that still need syncing, ordered by creation
// time so older records are reconciled first.
func (s *Store) PendingOffline() []OfflineRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]OfflineRecord, 0)
	for _, r := range s.offline {
		if r.Status == OfflinePending || r.Status == OfflineFailed {
			out = append(out, r)
		}
	}
	// stable ordering by created time then local id
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && beforeRec(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func beforeRec(a, b OfflineRecord) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.LocalID < b.LocalID
}

// OfflineRecord returns a record by LocalID.
func (s *Store) OfflineRecord(localID string) (OfflineRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.offline[localID]
	return r, ok
}

// OfflineReceipt returns a record by its receipt id.
func (s *Store) OfflineReceipt(receiptID string) (OfflineRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	localID, ok := s.offlineByReceipt[receiptID]
	if !ok {
		return OfflineRecord{}, false
	}
	return s.offline[localID], true
}

// MarkSyncing transitions a record to syncing under the write lock and returns
// a snapshot copy. It returns ok=false if the record is already being synced by
// another worker or has already been synced, which is what keeps concurrent
// reconcile passes from double-processing (and double-counting) a record.
func (s *Store) MarkSyncing(localID string) (OfflineRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.offline[localID]
	if !ok {
		return OfflineRecord{}, false
	}
	if r.Status == OfflineSyncing || r.Status == OfflineSynced {
		return r, false
	}
	r.Status = OfflineSyncing
	r.Attempts++
	s.offline[localID] = r
	return r, true
}

// MarkSynced confirms a record was applied server-side and records its receipt.
func (s *Store) MarkSynced(localID, receiptID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.offline[localID]
	if !ok {
		return
	}
	r.Status = OfflineSynced
	r.SyncedAt = s.clock.Now()
	r.ReceiptID = receiptID
	r.LastError = ""
	s.offline[localID] = r
	if receiptID != "" {
		s.offlineByReceipt[receiptID] = localID
	}
}

// MarkFailed records a sync failure and leaves the record pending retry.
func (s *Store) MarkFailed(localID, err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.offline[localID]
	if !ok {
		return
	}
	r.Status = OfflineFailed
	r.LastError = err
	s.offline[localID] = r
}

// OfflineStats summarises the offline cache for observability.
type OfflineStats struct {
	Pending int
	Syncing int
	Synced  int
	Failed  int
	Total   int
}

// OfflineStats returns aggregate counts of the offline cache.
func (s *Store) OfflineStats() OfflineStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := OfflineStats{Total: len(s.offline)}
	for _, r := range s.offline {
		switch r.Status {
		case OfflinePending:
			st.Pending++
		case OfflineSyncing:
			st.Syncing++
		case OfflineSynced:
			st.Synced++
		case OfflineFailed:
			st.Failed++
		}
	}
	return st
}
