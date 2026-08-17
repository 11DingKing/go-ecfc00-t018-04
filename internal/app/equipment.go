package app

import (
	"errors"
	"fmt"

	"qilian-patrol/internal/domain"
	"qilian-patrol/internal/store"
)

// ClaimEquipment locks equipment by inventory code for an officer using a
// priority-based contention rule. The result reports whether the claim was
// granted or queued (with a stable queue position) and carries a message
// prompting a losing officer to switch items.
func (s *Service) ClaimEquipment(code, officerID string, priority int) (domain.Equipment, domain.ClaimResult, error) {
	if code == "" || officerID == "" {
		return domain.Equipment{}, domain.ClaimResult{}, ErrOfficerRequired
	}
	e, ok := s.store.EquipmentByCode(code)
	if !ok {
		return domain.Equipment{}, domain.ClaimResult{}, domain.ErrEquipNotFound
	}
	eq, res, err := s.store.ClaimEquipment(e.ID, officerID, priority)
	if err != nil {
		return domain.Equipment{}, domain.ClaimResult{}, err
	}
	return eq, res, nil
}

// IssueEquipment finalizes a claim by issuing the equipment to an officer for a
// work order. If the work order is not in an issuable state (or the officer is
// not an assignee) the pending lock is rolled back immediately so inventory
// never holds a stale reservation.
func (s *Service) IssueEquipment(code, officerID, orderID string) (domain.Equipment, error) {
	e, ok := s.store.EquipmentByCode(code)
	if !ok {
		return domain.Equipment{}, domain.ErrEquipNotFound
	}
	w, ok := s.store.WorkOrder(orderID)
	if !ok {
		s.releaseEquipmentLock(e.ID)
		return domain.Equipment{}, fmt.Errorf("work order %s: %w", orderID, ErrOrderNotFound)
	}
	if !w.CanIssueEquipment() {
		s.releaseEquipmentLock(e.ID)
		return domain.Equipment{}, fmt.Errorf("work order %s in state %s: %w", orderID, w.Status, ErrCannotIssue)
	}
	if !contains(w.AssigneeIDs, officerID) {
		s.releaseEquipmentLock(e.ID)
		return domain.Equipment{}, ErrNotAssignee
	}
	return s.store.MutateEquipment(e.ID, func(eq *domain.Equipment, rx *store.LockedView) error {
		return eq.Issue(officerID, orderID, s.now())
	})
}

// releaseEquipmentLock rolls back a pending equipment reservation, promoting the
// next queued claim if any.
func (s *Service) releaseEquipmentLock(equipmentID string) {
	_, _ = s.store.MutateEquipment(equipmentID, func(eq *domain.Equipment, rx *store.LockedView) error {
		eq.ReleaseLock(s.now())
		return nil
	})
}

// ReturnEquipment records equipment being returned, enforcing that the return
// happens before the linked work order's shift ends and registering the
// equipment condition.
func (s *Service) ReturnEquipment(code, officerID string, cond domain.EquipmentCondition) (domain.Equipment, error) {
	e, ok := s.store.EquipmentByCode(code)
	if !ok {
		return domain.Equipment{}, domain.ErrEquipNotFound
	}
	now := s.now()
	return s.store.MutateEquipment(e.ID, func(eq *domain.Equipment, rx *store.LockedView) error {
		if eq.OrderID != "" {
			if w, ok := rx.WorkOrder(eq.OrderID); ok {
				if err := w.CanReturnEquipment(now); err != nil {
					return err
				}
			}
		}
		return eq.Return(officerID, cond, now)
	})
}

// Equipment retrieves equipment by code.
func (s *Service) Equipment(code string) (domain.Equipment, bool) {
	return s.store.EquipmentByCode(code)
}

// Service-level errors specific to equipment flows.
var (
	ErrCannotIssue   = errors.New("cannot issue equipment from this work order state")
	ErrOrderNotFound = errors.New("work order not found")
)
