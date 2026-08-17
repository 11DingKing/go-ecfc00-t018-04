// Package app implements the application orchestration for the patrol dispatch
// service. It wires HTTP requests to domain state transitions and persistence,
// enforcing cross-aggregate business rules that the domain cannot express
// alone: checkpoint-to-grid binding, equipment checkout with lock rollback,
// incident SLA handling, and offline record reconciliation.
package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"qilian-patrol/internal/domain"
	"qilian-patrol/internal/store"
)

// Service orchestrates all patrol dispatch use cases.
type Service struct {
	store      *store.Store
	sla        time.Duration
	deviationM float64
	gridWindow time.Duration
	coreMin    int
}

// New constructs a Service backed by the given store and tuned by the business
// rule scalars.
func New(s *store.Store, sla time.Duration, deviationM float64, gridWindow time.Duration, coreMin int) *Service {
	return &Service{
		store:      s,
		sla:        sla,
		deviationM: deviationM,
		gridWindow: gridWindow,
		coreMin:    coreMin,
	}
}

// FromStore exposes the underlying store for background workers.
func (s *Service) FromStore() *store.Store { return s.store }

// SLA returns the configured incident response SLA.
func (s *Service) SLA() time.Duration { return s.sla }

// DeviationMeters returns the configured off-route deviation threshold.
func (s *Service) DeviationMeters() float64 { return s.deviationM }

func (s *Service) now() time.Time { return s.store.Clock().Now() }

// newID generates a prefixed random id.
func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// CreateWorkOrderRequest captures the inputs for generating a weekly-plan work
// order.
type CreateWorkOrderRequest struct {
	GridID       string
	CheckpointID string
	AssigneeIDs  []string
	Priority     int
	Reported     bool
	PlannedStart time.Time
	ShiftEnd     time.Time
	RequestKey   string
}

// CreateWorkOrder generates a draft work order after enforcing the 24-hour grid
// reuse window. The request key makes generation idempotent.
func (s *Service) CreateWorkOrder(req CreateWorkOrderRequest) (domain.WorkOrder, error) {
	if req.GridID == "" {
		return domain.WorkOrder{}, errors.New("gridId is required")
	}
	if _, ok := s.store.Grid(req.GridID); !ok {
		return domain.WorkOrder{}, fmt.Errorf("grid %s: %w", req.GridID, ErrGridNotFound)
	}
	if req.CheckpointID != "" {
		cp, ok := s.store.Checkpoint(req.CheckpointID)
		if !ok {
			return domain.WorkOrder{}, fmt.Errorf("checkpoint %s: %w", req.CheckpointID, ErrCheckpointNotFound)
		}
		if cp.GridID != req.GridID {
			return domain.WorkOrder{}, ErrCheckpointGridMismatch
		}
	}
	w := domain.WorkOrder{
		ID:           newID("wo"),
		GridID:       req.GridID,
		CheckpointID: req.CheckpointID,
		Status:       domain.StatusDraft,
		AssigneeIDs:  append([]string(nil), req.AssigneeIDs...),
		Priority:     req.Priority,
		Reported:     req.Reported,
		PlannedStart: req.PlannedStart,
		ShiftEnd:     req.ShiftEnd,
	}
	stored, _, err := s.store.CreateWorkOrder(w, req.RequestKey, s.gridWindow)
	if err != nil {
		return domain.WorkOrder{}, err
	}
	return stored, nil
}

// Dispatch validates core-zone constraints and moves a draft order to
// dispatched.
func (s *Service) Dispatch(orderID string) (domain.WorkOrder, error) {
	return s.store.MutateWorkOrder(orderID, func(w *domain.WorkOrder, rx *store.LockedView) error {
		grid, ok := rx.Grid(w.GridID)
		if !ok {
			return fmt.Errorf("grid %s: %w", w.GridID, ErrGridNotFound)
		}
		if err := w.ValidateForDispatch(grid, s.coreMin); err != nil {
			return err
		}
		return w.Transition(domain.StatusDispatched, s.now())
	})
}

// CheckIn records an officer scanning a checkpoint QR code. The checkpoint must
// belong to the order's grid; on success the order moves to checked_in.
func (s *Service) CheckIn(orderID, officerID, qrCode string) (domain.WorkOrder, error) {
	if officerID == "" {
		return domain.WorkOrder{}, ErrOfficerRequired
	}
	cp, ok := s.store.CheckpointByQR(qrCode)
	if !ok {
		return domain.WorkOrder{}, ErrCheckpointNotFound
	}
	return s.store.MutateWorkOrder(orderID, func(w *domain.WorkOrder, rx *store.LockedView) error {
		if w.CheckpointID != "" && w.CheckpointID != cp.ID {
			return ErrCheckpointMismatch
		}
		if cp.GridID != w.GridID {
			return ErrCheckpointGridMismatch
		}
		if !contains(w.AssigneeIDs, officerID) {
			return ErrNotAssignee
		}
		w.CheckpointID = cp.ID
		return w.Transition(domain.StatusCheckedIn, s.now())
	})
}

// StartPatrol moves a checked-in order to in_progress.
func (s *Service) StartPatrol(orderID string) (domain.WorkOrder, error) {
	return s.store.MutateWorkOrder(orderID, func(w *domain.WorkOrder, rx *store.LockedView) error {
		return w.Transition(domain.StatusInProgress, s.now())
	})
}

// CompletePatrol moves an in_progress order to completed. All issued equipment
// must have been returned first.
func (s *Service) CompletePatrol(orderID string) (domain.WorkOrder, error) {
	return s.store.MutateWorkOrder(orderID, func(w *domain.WorkOrder, rx *store.LockedView) error {
		if open := openEquipmentForOrder(rx, orderID); open > 0 {
			return fmt.Errorf("%d equipment still issued: %w", open, ErrEquipmentOutstanding)
		}
		return w.Transition(domain.StatusCompleted, s.now())
	})
}

// VerifyPatrol moves a completed order to verified after the dispatcher
// reviews the submitted track.
func (s *Service) VerifyPatrol(orderID string) (domain.WorkOrder, error) {
	return s.store.MutateWorkOrder(orderID, func(w *domain.WorkOrder, rx *store.LockedView) error {
		if _, ok := rx.Track(orderID); !ok {
			return ErrTrackNotSubmitted
		}
		return w.Transition(domain.StatusVerified, s.now())
	})
}

// Cancel voids a non-terminal order.
func (s *Service) Cancel(orderID string) (domain.WorkOrder, error) {
	return s.store.MutateWorkOrder(orderID, func(w *domain.WorkOrder, rx *store.LockedView) error {
		return w.Transition(domain.StatusCancelled, s.now())
	})
}

// WorkOrder retrieves an order.
func (s *Service) WorkOrder(id string) (domain.WorkOrder, bool) { return s.store.WorkOrder(id) }

// WorkOrders lists all orders.
func (s *Service) WorkOrders() []domain.WorkOrder { return s.store.WorkOrders() }

// Grid retrieves a grid.
func (s *Service) Grid(id string) (domain.Grid, bool) { return s.store.Grid(id) }

// Grids lists all grids.
func (s *Service) Grids() []domain.Grid { return s.store.Grids() }

// SaveGrid persists a grid.
func (s *Service) SaveGrid(g domain.Grid) error {
	if err := g.Validate(); err != nil {
		return err
	}
	s.store.SaveGrid(g)
	return nil
}

// SaveCheckpoint persists a checkpoint.
func (s *Service) SaveCheckpoint(c domain.Checkpoint) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if _, ok := s.store.Grid(c.GridID); !ok {
		return ErrGridNotFound
	}
	s.store.SaveCheckpoint(c)
	return nil
}

// SaveEquipment persists equipment.
func (s *Service) SaveEquipment(e domain.Equipment) error {
	if err := e.Validate(); err != nil {
		return err
	}
	s.store.SaveEquipment(e)
	return nil
}

// EquipmentList returns all equipment.
func (s *Service) EquipmentList() []domain.Equipment { return s.store.EquipmentList() }

// openEquipmentForOrder counts equipment still issued to a work order, using
// the locked view so it is safe inside a mutation.
func openEquipmentForOrder(rx *store.LockedView, orderID string) int {
	count := 0
	for _, e := range rx.EquipmentList() {
		if e.Status == domain.EquipIssued && e.OrderID == orderID {
			count++
		}
	}
	return count
}

func contains(slice []string, v string) bool {
	for _, x := range slice {
		if x == v {
			return true
		}
	}
	return false
}

// Service-level sentinel errors.
var (
	ErrGridNotFound           = errors.New("grid not found")
	ErrCheckpointNotFound     = errors.New("checkpoint not found")
	ErrCheckpointMismatch     = errors.New("checkpoint does not match the work order")
	ErrCheckpointGridMismatch = errors.New("checkpoint does not belong to the work order grid")
	ErrNotAssignee            = errors.New("officer is not an assignee of this work order")
	ErrOfficerRequired        = errors.New("officer is required")
	ErrEquipmentOutstanding   = errors.New("equipment must be returned before completing patrol")
	ErrTrackNotSubmitted      = errors.New("track must be submitted before verification")
)
