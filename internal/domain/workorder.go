package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// WorkOrderStatus is the lifecycle state of a patrol work order.
type WorkOrderStatus string

const (
	// StatusDraft: generated but not yet dispatched by the dispatcher.
	StatusDraft WorkOrderStatus = "draft"
	// StatusDispatched: dispatcher has assigned the order to patrol officers.
	StatusDispatched WorkOrderStatus = "dispatched"
	// StatusCheckedIn: an officer has scanned a checkpoint to sign in.
	StatusCheckedIn WorkOrderStatus = "checked_in"
	// StatusInProgress: patrol is underway on the grid.
	StatusInProgress WorkOrderStatus = "in_progress"
	// StatusCompleted: patrol finished, equipment returned, track submitted.
	StatusCompleted WorkOrderStatus = "completed"
	// StatusVerified: dispatcher has verified the submitted patrol track.
	StatusVerified WorkOrderStatus = "verified"
	// StatusCancelled: order voided before completion.
	StatusCancelled WorkOrderStatus = "cancelled"
)

// IsTerminal reports whether the status is a final state.
func (s WorkOrderStatus) IsTerminal() bool {
	return s == StatusVerified || s == StatusCancelled
}

// IsActive reports whether the order counts as a valid (non-cancelled) order
// occupying a grid within the 24-hour reuse window.
func (s WorkOrderStatus) IsActive() bool {
	switch s {
	case StatusDraft, StatusDispatched, StatusCheckedIn, StatusInProgress, StatusCompleted:
		return true
	}
	return false
}

// WorkOrder is a weekly-plan patrol assignment for a single grid.
type WorkOrder struct {
	ID           string          `json:"id"`
	GridID       string          `json:"gridId"`
	CheckpointID string          `json:"checkpointId"`
	Status       WorkOrderStatus `json:"status"`
	AssigneeIDs  []string        `json:"assigneeIds"`
	Priority     int             `json:"priority"`
	Reported     bool            `json:"reported"` // prior report filed (required for core zone)
	PlannedStart time.Time       `json:"plannedStart"`
	ShiftEnd     time.Time       `json:"shiftEnd"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	IssueCount   int             `json:"issueCount"` // number of equipment items issued
}

// transitions defines the legal forward state transitions for a work order.
var transitions = map[WorkOrderStatus][]WorkOrderStatus{
	StatusDraft:      {StatusDispatched, StatusCancelled},
	StatusDispatched: {StatusCheckedIn, StatusCancelled},
	StatusCheckedIn:  {StatusInProgress, StatusCancelled},
	StatusInProgress: {StatusCompleted, StatusCancelled},
	StatusCompleted:  {StatusVerified, StatusCancelled},
	StatusVerified:   {},
	StatusCancelled:  {},
}

// canTransition reports whether moving from one status to another is legal.
func canTransition(from, to WorkOrderStatus) bool {
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	return slices.Contains(allowed, to)
}

// Transition moves the work order to the next status after validating legality.
func (w *WorkOrder) Transition(to WorkOrderStatus, now time.Time) error {
	if w.Status.IsTerminal() {
		return fmt.Errorf("work order %s: %w", w.ID, ErrOrderTerminal)
	}
	if !canTransition(w.Status, to) {
		return fmt.Errorf("work order %s: illegal transition %s -> %s: %w", w.ID, w.Status, to, ErrInvalidTransition)
	}
	w.Status = to
	w.UpdatedAt = now
	return nil
}

// ValidateForDispatch enforces business rules that must hold before a work
// order is dispatched. The grid is passed in so core-zone constraints can be
// evaluated.
func (w WorkOrder) ValidateForDispatch(g Grid, minAssignees int) error {
	if w.ID == "" {
		return ErrOrderIDRequired
	}
	if w.GridID == "" {
		return ErrOrderGridRequired
	}
	if w.GridID != g.ID {
		return fmt.Errorf("work order %s: grid %s does not match grid %s: %w", w.ID, w.GridID, g.ID, ErrGridMismatch)
	}
	if len(w.AssigneeIDs) == 0 {
		return ErrNoAssignees
	}
	if g.IsCoreZone() {
		if len(w.AssigneeIDs) < minAssignees {
			return fmt.Errorf("core zone %s requires at least %d assignees: %w", g.ID, minAssignees, ErrCoreZoneAssignees)
		}
		if !w.Reported {
			return fmt.Errorf("core zone %s requires prior report: %w", g.ID, ErrCoreZoneReport)
		}
	}
	if !w.PlannedStart.IsZero() && !w.ShiftEnd.IsZero() && !w.ShiftEnd.After(w.PlannedStart) {
		return ErrShiftEndBeforeStart
	}
	return nil
}

// CanIssueEquipment reports whether an order is in a state where equipment may
// be issued (officer has checked in and patrol has not completed).
func (w WorkOrder) CanIssueEquipment() bool {
	switch w.Status {
	case StatusCheckedIn, StatusInProgress:
		return true
	}
	return false
}

// CanReturnEquipment reports whether the order is in a state where equipment
// return is permitted (before the shift ends).
func (w WorkOrder) CanReturnEquipment(now time.Time) error {
	switch w.Status {
	case StatusCheckedIn, StatusInProgress, StatusCompleted:
	default:
		return fmt.Errorf("cannot return equipment from status %s: %w", w.Status, ErrInvalidTransition)
	}
	if !w.ShiftEnd.IsZero() && now.After(w.ShiftEnd) {
		return ErrReturnAfterShift
	}
	return nil
}

// DuplicateWindow reports whether an existing order for the same grid still
// occupies the 24-hour reuse window relative to now.
func (w WorkOrder) DuplicateWindow(existing WorkOrder, window time.Duration, now time.Time) bool {
	if !existing.Status.IsActive() {
		return false
	}
	if existing.GridID != w.GridID {
		return false
	}
	cutoff := now.Add(-window)
	return existing.CreatedAt.After(cutoff) || existing.CreatedAt.Equal(cutoff)
}

// Work order domain errors.
var (
	ErrOrderIDRequired     = errors.New("work order: id is required")
	ErrOrderGridRequired   = errors.New("work order: gridId is required")
	ErrNoAssignees         = errors.New("work order: at least one assignee required")
	ErrGridMismatch        = errors.New("work order grid does not match grid")
	ErrCoreZoneAssignees   = errors.New("core zone requires multiple assignees")
	ErrCoreZoneReport      = errors.New("core zone requires prior report")
	ErrShiftEndBeforeStart = errors.New("shiftEnd must be after plannedStart")
	ErrInvalidTransition   = errors.New("invalid state transition")
	ErrOrderTerminal       = errors.New("work order is terminal")
	ErrReturnAfterShift    = errors.New("equipment return after shift end is not allowed")
	ErrDuplicateOrder      = errors.New("a valid work order already exists for this grid within the reuse window")
)
