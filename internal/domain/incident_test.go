package domain

import (
	"errors"
	"testing"
	"time"
)

func TestIncidentStateFlow(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	in := Incident{ID: "inc", WorkOrderID: "wo", Type: IncidentFire, IssueID: "issue-1", Status: IncidentReported, ReportedAt: now}
	if err := in.Assign("handler-1", 30*time.Minute, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if in.SLABreached {
		t.Fatal("within SLA should not be breached")
	}
	if err := in.StartHandling(now.Add(15 * time.Minute)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if err := in.Close(now.Add(40 * time.Minute)); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !in.Status.IsClosed() {
		t.Fatal("expected closed")
	}
}

func TestIncidentSLABreachOnAssign(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	in := Incident{ID: "inc", Type: IncidentPest, IssueID: "issue-2", Status: IncidentReported, ReportedAt: now}
	// Assignment 45 minutes later breaches the 30-minute SLA but still succeeds.
	if err := in.Assign("handler-1", 30*time.Minute, now.Add(45*time.Minute)); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !in.SLABreached {
		t.Fatal("expected SLABreached to be flagged")
	}
}

func TestIncidentIllegalClose(t *testing.T) {
	in := Incident{ID: "inc", Type: IncidentFire, IssueID: "issue-3", Status: IncidentReported}
	if err := in.Close(time.Now()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestIncidentIsSLABreached(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	in := Incident{Status: IncidentReported, ReportedAt: now}
	if in.IsSLABreached(30*time.Minute, now.Add(20*time.Minute)) {
		t.Fatal("20min should not breach 30min SLA")
	}
	if !in.IsSLABreached(30*time.Minute, now.Add(31*time.Minute)) {
		t.Fatal("31min should breach 30min SLA")
	}
}
