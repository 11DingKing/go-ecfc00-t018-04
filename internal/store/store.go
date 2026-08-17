// Package store provides a thread-safe in-memory persistence layer for the
// patrol dispatch service. It is the single source of truth for aggregate state
// and enforces cross-aggregate invariants that must hold atomically: the 24-hour
// grid reuse window, equipment contention, and idempotent incident creation.
//
// All aggregates share a single sync.RWMutex. Mutations run under the write
// lock and receive a LockedView whose accessors perform no locking, so a
// mutation can safely read other aggregates without re-entering the lock (which
// would deadlock). Public read methods take the read lock for caller
// convenience.
package store

import (
	"errors"
	"sync"
	"time"

	"qilian-patrol/internal/domain"
)

// Clock abstracts wall-clock time so background workers and tests are
// deterministic.
type Clock interface {
	Now() time.Time
}

// RealClock returns the actual wall clock.
type RealClock struct{}

// Now returns time.Now.
func (RealClock) Now() time.Time { return time.Now() }

// Store is a concurrency-safe in-memory repository for all aggregates.
type Store struct {
	mu               sync.RWMutex
	clock            Clock
	grids            map[string]domain.Grid
	checkpoints      map[string]domain.Checkpoint
	orders           map[string]domain.WorkOrder
	equipment        map[string]domain.Equipment
	incidents        map[string]domain.Incident
	incidentByIssue  map[string]string
	tracks           map[string]domain.PatrolTrack
	offline          map[string]OfflineRecord
	offlineByReceipt map[string]string
	idem             map[string]string
}

// New constructs an empty Store using the given clock. A nil clock falls back
// to the real wall clock.
func New(clock Clock) *Store {
	if clock == nil {
		clock = RealClock{}
	}
	return &Store{
		clock:            clock,
		grids:            make(map[string]domain.Grid),
		checkpoints:      make(map[string]domain.Checkpoint),
		orders:           make(map[string]domain.WorkOrder),
		equipment:        make(map[string]domain.Equipment),
		incidents:        make(map[string]domain.Incident),
		incidentByIssue:  make(map[string]string),
		tracks:           make(map[string]domain.PatrolTrack),
		offline:          make(map[string]OfflineRecord),
		offlineByReceipt: make(map[string]string),
		idem:             make(map[string]string),
	}
}

// Clock returns the store's clock.
func (s *Store) Clock() Clock { return s.clock }

// LockedView exposes unlocked read accessors for use inside a mutation that
// already holds the write lock. Calling these outside a mutation is unsafe.
type LockedView struct{ s *Store }

// Grid returns a grid by id without locking.
func (v *LockedView) Grid(id string) (domain.Grid, bool) {
	g, ok := v.s.grids[id]
	return g, ok
}

// WorkOrder returns a work order by id without locking.
func (v *LockedView) WorkOrder(id string) (domain.WorkOrder, bool) {
	w, ok := v.s.orders[id]
	return w, ok
}

// EquipmentList returns all equipment without locking.
func (v *LockedView) EquipmentList() []domain.Equipment {
	out := make([]domain.Equipment, 0, len(v.s.equipment))
	for _, e := range v.s.equipment {
		out = append(out, e)
	}
	return out
}

// Track returns a track by work order id without locking.
func (v *LockedView) Track(workOrderID string) (domain.PatrolTrack, bool) {
	t, ok := v.s.tracks[workOrderID]
	return t, ok
}

// Incident returns an incident by id without locking.
func (v *LockedView) Incident(id string) (domain.Incident, bool) {
	in, ok := v.s.incidents[id]
	return in, ok
}

// --- Grids ---

// SaveGrid upserts a grid.
func (s *Store) SaveGrid(g domain.Grid) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.grids[g.ID] = g
}

// Grid returns a grid by id.
func (s *Store) Grid(id string) (domain.Grid, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.grids[id]
	return g, ok
}

// Grids returns all grids.
func (s *Store) Grids() []domain.Grid {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Grid, 0, len(s.grids))
	for _, g := range s.grids {
		out = append(out, g)
	}
	return out
}

// --- Checkpoints ---

// SaveCheckpoint upserts a checkpoint.
func (s *Store) SaveCheckpoint(c domain.Checkpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkpoints[c.ID] = c
}

// Checkpoint returns a checkpoint by id.
func (s *Store) Checkpoint(id string) (domain.Checkpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.checkpoints[id]
	return c, ok
}

// CheckpointByQR looks up a checkpoint by its QR code.
func (s *Store) CheckpointByQR(qr string) (domain.Checkpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.checkpoints {
		if c.QRCode == qr {
			return c, true
		}
	}
	return domain.Checkpoint{}, false
}

// --- Work orders ---

// SaveWorkOrder upserts a work order.
func (s *Store) SaveWorkOrder(w domain.WorkOrder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[w.ID] = w
}

// WorkOrder returns a work order by id.
func (s *Store) WorkOrder(id string) (domain.WorkOrder, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.orders[id]
	return w, ok
}

// WorkOrders returns all work orders.
func (s *Store) WorkOrders() []domain.WorkOrder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.WorkOrder, 0, len(s.orders))
	for _, w := range s.orders {
		out = append(out, w)
	}
	return out
}

// CreateWorkOrder atomically enforces the 24-hour grid reuse window and an
// optional idempotency key. It returns the stored order (existing one if the
// request key already succeeded) and a flag indicating whether the order was
// newly created.
func (s *Store) CreateWorkOrder(w domain.WorkOrder, requestKey string, window time.Duration) (domain.WorkOrder, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if requestKey != "" {
		if id, ok := s.idem[requestKey]; ok {
			return s.orders[id], false, nil
		}
	}
	now := s.clock.Now()
	for _, existing := range s.orders {
		if existing.GridID != w.GridID || !existing.Status.IsActive() {
			continue
		}
		if now.Sub(existing.CreatedAt) < window {
			return domain.WorkOrder{}, false, domain.ErrDuplicateOrder
		}
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = now
	}
	w.UpdatedAt = now
	s.orders[w.ID] = w
	if requestKey != "" {
		s.idem[requestKey] = w.ID
	}
	return w, true, nil
}

// MutateWorkOrder loads a work order, applies fn under the write lock, and
// stores the result. fn receives a LockedView for safe cross-aggregate reads.
func (s *Store) MutateWorkOrder(id string, fn func(*domain.WorkOrder, *LockedView) error) (domain.WorkOrder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.orders[id]
	if !ok {
		return domain.WorkOrder{}, ErrOrderNotFound
	}
	v := &LockedView{s: s}
	if err := fn(&w, v); err != nil {
		return domain.WorkOrder{}, err
	}
	w.UpdatedAt = s.clock.Now()
	s.orders[id] = w
	return w, nil
}

// --- Equipment ---

// SaveEquipment upserts equipment.
func (s *Store) SaveEquipment(e domain.Equipment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.equipment[e.ID] = e
}

// Equipment returns equipment by id.
func (s *Store) Equipment(id string) (domain.Equipment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.equipment[id]
	return e, ok
}

// EquipmentByCode returns equipment by inventory code.
func (s *Store) EquipmentByCode(code string) (domain.Equipment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.equipment {
		if e.Code == code {
			return e, true
		}
	}
	return domain.Equipment{}, false
}

// EquipmentList returns all equipment.
func (s *Store) EquipmentList() []domain.Equipment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Equipment, 0, len(s.equipment))
	for _, e := range s.equipment {
		out = append(out, e)
	}
	return out
}

// ClaimEquipment atomically claims equipment for an officer using a
// priority-based contention rule. The whole load-mutate-store cycle runs under
// the write lock so concurrent claims are ordered deterministically.
func (s *Store) ClaimEquipment(id, officerID string, priority int) (domain.Equipment, domain.ClaimResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.equipment[id]
	if !ok {
		return domain.Equipment{}, domain.ClaimResult{}, domain.ErrEquipNotFound
	}
	res := e.Claim(officerID, priority, s.clock.Now())
	s.equipment[id] = e
	return e, res, nil
}

// MutateEquipment loads equipment, applies fn under the write lock, and stores
// the result. fn receives a LockedView for safe cross-aggregate reads.
func (s *Store) MutateEquipment(id string, fn func(*domain.Equipment, *LockedView) error) (domain.Equipment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.equipment[id]
	if !ok {
		return domain.Equipment{}, domain.ErrEquipNotFound
	}
	v := &LockedView{s: s}
	if err := fn(&e, v); err != nil {
		return domain.Equipment{}, err
	}
	s.equipment[id] = e
	return e, nil
}

// --- Incidents ---

// SaveIncident upserts an incident and indexes it by IssueID.
func (s *Store) SaveIncident(in domain.Incident) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.incidents[in.ID] = in
	if in.IssueID != "" {
		s.incidentByIssue[in.IssueID] = in.ID
	}
}

// Incident returns an incident by id.
func (s *Store) Incident(id string) (domain.Incident, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.incidents[id]
	return in, ok
}

// IncidentByIssue returns an incident by its idempotency issue id.
func (s *Store) IncidentByIssue(issueID string) (domain.Incident, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.incidentByIssue[issueID]
	if !ok {
		return domain.Incident{}, false
	}
	return s.incidents[id], true
}

// OpenIncidents returns incidents that have not reached a terminal state.
func (s *Store) OpenIncidents() []domain.Incident {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Incident, 0)
	for _, in := range s.incidents {
		if !in.Status.IsClosed() {
			out = append(out, in)
		}
	}
	return out
}

// Incidents returns all incidents.
func (s *Store) Incidents() []domain.Incident {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Incident, 0, len(s.incidents))
	for _, in := range s.incidents {
		out = append(out, in)
	}
	return out
}

// PutIncidentAtomic loads-or-creates an incident by IssueID under the write
// lock, guaranteeing idempotent offline re-submission.
func (s *Store) PutIncidentAtomic(in domain.Incident) (domain.Incident, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.IssueID != "" {
		if id, ok := s.incidentByIssue[in.IssueID]; ok {
			return s.incidents[id], false, nil
		}
	}
	if in.ReportedAt.IsZero() {
		in.ReportedAt = s.clock.Now()
	}
	s.incidents[in.ID] = in
	if in.IssueID != "" {
		s.incidentByIssue[in.IssueID] = in.ID
	}
	return in, true, nil
}

// MutateIncident loads an incident, applies fn under the write lock, and stores
// the result.
func (s *Store) MutateIncident(id string, fn func(*domain.Incident, *LockedView) error) (domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.incidents[id]
	if !ok {
		return domain.Incident{}, domain.ErrIncidentNotFound
	}
	v := &LockedView{s: s}
	if err := fn(&in, v); err != nil {
		return domain.Incident{}, err
	}
	s.incidents[id] = in
	if in.IssueID != "" {
		s.incidentByIssue[in.IssueID] = in.ID
	}
	return in, nil
}

// --- Tracks ---

// SaveTrack upserts a patrol track.
func (s *Store) SaveTrack(t domain.PatrolTrack) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tracks[t.WorkOrderID] = t
}

// Track returns a track by work order id.
func (s *Store) Track(workOrderID string) (domain.PatrolTrack, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tracks[workOrderID]
	return t, ok
}

// ErrOrderNotFound is returned when a work order lookup misses.
var ErrOrderNotFound = errors.New("work order not found")
