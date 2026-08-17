package domain

import (
	"errors"
	"testing"
	"time"
)

func TestWorkOrderTransitionHappyPath(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	w := WorkOrder{ID: "wo-1", Status: StatusDraft}
	steps := []WorkOrderStatus{StatusDispatched, StatusCheckedIn, StatusInProgress, StatusCompleted, StatusVerified}
	for _, want := range steps {
		if err := w.Transition(want, now.Add(time.Second)); err != nil {
			t.Fatalf("transition to %s: %v", want, err)
		}
		if w.Status != want {
			t.Fatalf("got %s want %s", w.Status, want)
		}
	}
}

func TestWorkOrderIllegalTransition(t *testing.T) {
	now := time.Now()
	w := WorkOrder{ID: "wo-1", Status: StatusDraft}
	if err := w.Transition(StatusInProgress, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
	// Terminal state rejects further transitions.
	w.Status = StatusVerified
	if err := w.Transition(StatusCompleted, now); !errors.Is(err, ErrOrderTerminal) {
		t.Fatalf("expected ErrOrderTerminal, got %v", err)
	}
}

func TestValidateForDispatchCoreZone(t *testing.T) {
	core := Grid{ID: "g-core", Name: "core", ZoneType: ZoneCore, Route: []Point{{1, 1}, {2, 2}}}
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		order   WorkOrder
		wantErr error
	}{
		{
			"too few assignees",
			WorkOrder{ID: "wo", GridID: "g-core", AssigneeIDs: []string{"a1"}, Reported: true, PlannedStart: now, ShiftEnd: now.Add(8 * time.Hour)},
			ErrCoreZoneAssignees,
		},
		{
			"missing prior report",
			WorkOrder{ID: "wo", GridID: "g-core", AssigneeIDs: []string{"a1", "a2"}, Reported: false, PlannedStart: now, ShiftEnd: now.Add(8 * time.Hour)},
			ErrCoreZoneReport,
		},
		{
			"valid core zone",
			WorkOrder{ID: "wo", GridID: "g-core", AssigneeIDs: []string{"a1", "a2"}, Reported: true, PlannedStart: now, ShiftEnd: now.Add(8 * time.Hour)},
			nil,
		},
		{
			"shift end before start",
			WorkOrder{ID: "wo", GridID: "g-core", AssigneeIDs: []string{"a1", "a2"}, Reported: true, PlannedStart: now, ShiftEnd: now.Add(-time.Hour)},
			ErrShiftEndBeforeStart,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.order.ValidateForDispatch(core, 2)
			if c.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("got %v want %v", err, c.wantErr)
			}
		})
	}
}

func TestDuplicateWindow(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	existing := WorkOrder{GridID: "g1", Status: StatusDispatched, CreatedAt: now.Add(-2 * time.Hour)}
	window := 24 * time.Hour
	if !existing.DuplicateWindow(existing, window, now) {
		t.Fatal("expected duplicate within 24h")
	}
	old := WorkOrder{GridID: "g1", Status: StatusDispatched, CreatedAt: now.Add(-25 * time.Hour)}
	if old.DuplicateWindow(old, window, now) {
		t.Fatal("expected no duplicate beyond 24h")
	}
	cancelled := WorkOrder{GridID: "g1", Status: StatusCancelled, CreatedAt: now.Add(-time.Hour)}
	if cancelled.DuplicateWindow(cancelled, window, now) {
		t.Fatal("cancelled order should not occupy the window")
	}
}

func TestCanReturnEquipmentShiftEnd(t *testing.T) {
	now := time.Date(2026, 8, 17, 18, 1, 0, 0, time.UTC)
	w := WorkOrder{Status: StatusInProgress, ShiftEnd: now.Add(-time.Minute)}
	if err := w.CanReturnEquipment(now); !errors.Is(err, ErrReturnAfterShift) {
		t.Fatalf("expected ErrReturnAfterShift, got %v", err)
	}
	w.ShiftEnd = now.Add(time.Hour)
	if err := w.CanReturnEquipment(now); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	w.Status = StatusDraft
	if err := w.CanReturnEquipment(now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}
