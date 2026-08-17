package domain

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// EquipmentStatus is the lifecycle state of a piece of patrol equipment.
type EquipmentStatus string

const (
	// EquipAvailable: idle and claimable.
	EquipAvailable EquipmentStatus = "available"
	// EquipLocked: reserved by an officer pending issue; still contestable by a
	// higher-priority claim until issue completes.
	EquipLocked EquipmentStatus = "locked"
	// EquipIssued: handed to an officer on patrol.
	EquipIssued EquipmentStatus = "issued"
	// EquipReturned: returned at the end of a shift.
	EquipReturned EquipmentStatus = "returned"
	// EquipRetired: damaged beyond use and removed from inventory.
	EquipRetired EquipmentStatus = "retired"
)

// EquipmentCondition records the integrity state when equipment is returned.
type EquipmentCondition string

const (
	CondGood    EquipmentCondition = "good"
	CondDamaged EquipmentCondition = "damaged"
)

// QueueEntry is an officer waiting for contested equipment, ordered by
// reporting priority (higher wins) then by claim time (earlier wins).
type QueueEntry struct {
	OfficerID string
	Priority  int
	ClaimedAt time.Time
}

// Equipment is a numbered piece of patrol gear (e.g. radio, GPS tracker).
type Equipment struct {
	ID        string             `json:"id"`
	Code      string             `json:"code"` // human-readable inventory number
	Name      string             `json:"name"`
	Status    EquipmentStatus    `json:"status"`
	HolderID  string             `json:"holderId"`
	Priority  int                `json:"priority"`
	Condition EquipmentCondition `json:"condition"`
	OrderID   string             `json:"orderId"`
	LockedAt  time.Time          `json:"lockedAt"`
	Queue     []QueueEntry       `json:"queue"`
	UpdatedAt time.Time          `json:"updatedAt"`
}

// ClaimResult is the outcome of attempting to claim equipment.
type ClaimResult struct {
	Granted  bool
	HolderID string
	Position int    // 1-based queue position when not granted
	Message  string // prompt for the losing officer, e.g. "switch equipment"
}

// Claim attempts to reserve equipment for an officer. When two officers contend
// for the same available item, the higher reporting priority wins; the loser is
// queued with a stable position and prompted to switch items. Preemption only
// applies while the item is still locked (not yet issued), modelling the brief
// contention window described by the business rules.
func (e *Equipment) Claim(officerID string, priority int, now time.Time) ClaimResult {
	if e.Status == EquipRetired {
		return ClaimResult{Message: "equipment retired, please switch"}
	}
	if e.Status == EquipAvailable {
		e.Status = EquipLocked
		e.HolderID = officerID
		e.Priority = priority
		e.LockedAt = now
		e.UpdatedAt = now
		return ClaimResult{Granted: true, HolderID: officerID}
	}
	// Contention: a higher reporting priority may preempt a held-but-unissued
	// lock. The displaced holder is moved to the front of the wait queue.
	if e.Status == EquipLocked && priority > e.Priority {
		e.Queue = append([]QueueEntry{{OfficerID: e.HolderID, Priority: e.Priority, ClaimedAt: e.LockedAt}}, e.Queue...)
		e.HolderID = officerID
		e.Priority = priority
		e.LockedAt = now
		e.UpdatedAt = now
		return ClaimResult{Granted: true, HolderID: officerID}
	}
	// Otherwise enqueue the contending officer preserving queue order.
	entry := QueueEntry{OfficerID: officerID, Priority: priority, ClaimedAt: now}
	e.Queue = append(e.Queue, entry)
	e.sortQueue()
	e.UpdatedAt = now
	pos := e.queuePosition(officerID)
	return ClaimResult{Granted: false, Position: pos, Message: "equipment held by higher priority, please switch or wait"}
}

// Issue finalizes a lock, marking the equipment as handed to the holder for a
// given work order. Only the current holder may issue.
func (e *Equipment) Issue(officerID, orderID string, now time.Time) error {
	if e.Status != EquipLocked {
		return fmt.Errorf("equipment %s: not locked (status=%s): %w", e.ID, e.Status, ErrEquipNotLocked)
	}
	if e.HolderID != officerID {
		return fmt.Errorf("equipment %s: officer %s is not the holder: %w", e.ID, officerID, ErrNotHolder)
	}
	e.Status = EquipIssued
	e.OrderID = orderID
	e.UpdatedAt = now
	return nil
}

// ReleaseLock rolls back a pending lock, returning the item to availability and
// promoting the next queued officer. This is the inventory rollback path used
// when issue fails or a work order is cancelled after a lock.
func (e *Equipment) ReleaseLock(now time.Time) {
	if e.Status != EquipLocked {
		return
	}
	e.OrderID = ""
	e.promoteNext(now)
}

// Return records equipment being handed back, capturing its condition. After a
// return the next queued claim (if any) is promoted.
func (e *Equipment) Return(officerID string, cond EquipmentCondition, now time.Time) error {
	if e.Status != EquipIssued {
		return fmt.Errorf("equipment %s: not issued (status=%s): %w", e.ID, e.Status, ErrEquipNotIssued)
	}
	if e.HolderID != officerID {
		return fmt.Errorf("equipment %s: officer %s did not issue this equipment: %w", e.ID, officerID, ErrNotHolder)
	}
	e.Status = EquipReturned
	e.Condition = cond
	e.OrderID = ""
	e.UpdatedAt = now
	if cond == CondDamaged {
		e.Status = EquipRetired
	}
	e.promoteNext(now)
	return nil
}

// promoteNext awards a freed item to the highest-priority queued officer.
func (e *Equipment) promoteNext(now time.Time) {
	if len(e.Queue) == 0 {
		e.Status = EquipAvailable
		e.HolderID = ""
		e.Priority = 0
		e.LockedAt = time.Time{}
		return
	}
	e.sortQueue()
	next := e.Queue[0]
	e.Queue = e.Queue[1:]
	e.Status = EquipLocked
	e.HolderID = next.OfficerID
	e.Priority = next.Priority
	e.LockedAt = now
}

func (e *Equipment) sortQueue() {
	sort.SliceStable(e.Queue, func(i, j int) bool {
		if e.Queue[i].Priority != e.Queue[j].Priority {
			return e.Queue[i].Priority > e.Queue[j].Priority
		}
		return e.Queue[i].ClaimedAt.Before(e.Queue[j].ClaimedAt)
	})
}

func (e *Equipment) queuePosition(officerID string) int {
	for i, q := range e.Queue {
		if q.OfficerID == officerID {
			return i + 1
		}
	}
	return 0
}

// Validate performs structural validation of equipment.
func (e Equipment) Validate() error {
	if e.ID == "" {
		return errors.New("equipment: id is required")
	}
	if e.Code == "" {
		return errors.New("equipment: code is required")
	}
	if e.Status == "" {
		e.Status = EquipAvailable
	}
	return nil
}

// Equipment domain errors.
var (
	ErrEquipNotLocked = errors.New("equipment is not locked")
	ErrEquipNotIssued = errors.New("equipment is not issued")
	ErrNotHolder      = errors.New("officer is not the equipment holder")
	ErrEquipNotFound  = errors.New("equipment not found")
)
