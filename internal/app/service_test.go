package app

import (
	"errors"
	"sync"
	"testing"
	"time"

	"qilian-patrol/internal/domain"
	"qilian-patrol/internal/store"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time      { return f.t }
func (f *fakeClock) Add(d time.Duration) { f.t = f.t.Add(d) }

func newService() (*Service, *fakeClock, *store.Store) {
	c := &fakeClock{t: time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)}
	st := store.New(c)
	svc := New(st, 30*time.Minute, 200, 24*time.Hour, 2)
	return svc, c, st
}

func seed(svc *Service, clk *fakeClock) {
	svc.SaveGrid(domain.Grid{ID: "g-general", Name: "general", ZoneType: domain.ZoneGeneral, Route: []domain.Point{{Lat: 38.80, Lng: 100.30}, {Lat: 38.805, Lng: 100.305}}})
	svc.SaveGrid(domain.Grid{ID: "g-core", Name: "core", ZoneType: domain.ZoneCore, Route: []domain.Point{{Lat: 38.90, Lng: 100.40}, {Lat: 38.905, Lng: 100.405}}})
	svc.SaveCheckpoint(domain.Checkpoint{ID: "cp-1", GridID: "g-general", QRCode: "QR-1"})
	svc.SaveCheckpoint(domain.Checkpoint{ID: "cp-2", GridID: "g-core", QRCode: "QR-2"})
	svc.SaveEquipment(domain.Equipment{ID: "eq-1", Code: "RADIO-1", Name: "radio", Status: domain.EquipAvailable})
	svc.SaveEquipment(domain.Equipment{ID: "eq-2", Code: "RADIO-2", Name: "radio", Status: domain.EquipAvailable})
}

func TestFullPatrolWorkflow(t *testing.T) {
	svc, clk, _ := newService()
	seed(svc, clk)
	start := clk.Now()
	wo, err := svc.CreateWorkOrder(CreateWorkOrderRequest{
		GridID:       "g-general",
		AssigneeIDs:  []string{"o-1"},
		Priority:     5,
		PlannedStart: start,
		ShiftEnd:     start.Add(8 * time.Hour),
		RequestKey:   "rk-1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if wo.Status != domain.StatusDraft {
		t.Fatalf("expected draft, got %s", wo.Status)
	}
	if _, err := svc.Dispatch(wo.ID); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err := svc.CheckIn(wo.ID, "o-1", "QR-1"); err != nil {
		t.Fatalf("checkin: %v", err)
	}
	// Claim and issue equipment.
	if _, res, err := svc.ClaimEquipment("RADIO-1", "o-1", 5); err != nil || !res.Granted {
		t.Fatalf("claim: err=%v res=%+v", err, res)
	}
	if _, err := svc.IssueEquipment("RADIO-1", "o-1", wo.ID); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := svc.StartPatrol(wo.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Completing with outstanding equipment must fail.
	if _, err := svc.CompletePatrol(wo.ID); !errors.Is(err, ErrEquipmentOutstanding) {
		t.Fatalf("expected ErrEquipmentOutstanding, got %v", err)
	}
	// Return and submit track.
	if _, err := svc.ReturnEquipment("RADIO-1", "o-1", domain.CondGood); err != nil {
		t.Fatalf("return: %v", err)
	}
	track, err := svc.SubmitTrack(wo.ID, []domain.TrackPoint{
		{Lat: 38.80, Lng: 100.30, RecordedAt: clk.Now()},
		{Lat: 38.805, Lng: 100.305, RecordedAt: clk.Now().Add(time.Minute)},
	}, false)
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	if track.Deviation {
		t.Fatal("on-route track should not deviate")
	}
	if _, err := svc.CompletePatrol(wo.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := svc.VerifyPatrol(wo.ID); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestCoreZoneRequiresReportAndPair(t *testing.T) {
	svc, clk, _ := newService()
	seed(svc, clk)
	start := clk.Now()
	// Missing prior report: dispatch should reject for a core zone.
	wo, _ := svc.CreateWorkOrder(CreateWorkOrderRequest{
		GridID:       "g-core",
		AssigneeIDs:  []string{"o-1", "o-2"},
		Reported:     false,
		PlannedStart: start,
		ShiftEnd:     start.Add(8 * time.Hour),
	})
	if _, err := svc.Dispatch(wo.ID); !errors.Is(err, domain.ErrCoreZoneReport) {
		t.Fatalf("expected ErrCoreZoneReport, got %v", err)
	}
	// Cancel the first order so the 24-hour reuse window frees the grid.
	if _, err := svc.Cancel(wo.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// Reported but single assignee: still rejected.
	wo2, _ := svc.CreateWorkOrder(CreateWorkOrderRequest{
		GridID:       "g-core",
		AssigneeIDs:  []string{"o-1"},
		Reported:     true,
		PlannedStart: start,
		ShiftEnd:     start.Add(8 * time.Hour),
	})
	if _, err := svc.Dispatch(wo2.ID); !errors.Is(err, domain.ErrCoreZoneAssignees) {
		t.Fatalf("expected ErrCoreZoneAssignees, got %v", err)
	}
}

func TestDuplicateGridOrderRejected(t *testing.T) {
	svc, clk, _ := newService()
	seed(svc, clk)
	start := clk.Now()
	_, err := svc.CreateWorkOrder(CreateWorkOrderRequest{
		GridID: "g-general", AssigneeIDs: []string{"o-1"}, PlannedStart: start, ShiftEnd: start.Add(8 * time.Hour), RequestKey: "k1",
	})
	if err != nil {
		t.Fatalf("create1: %v", err)
	}
	_, err = svc.CreateWorkOrder(CreateWorkOrderRequest{
		GridID: "g-general", AssigneeIDs: []string{"o-2"}, PlannedStart: start, ShiftEnd: start.Add(8 * time.Hour), RequestKey: "k2",
	})
	if !errors.Is(err, domain.ErrDuplicateOrder) {
		t.Fatalf("expected ErrDuplicateOrder, got %v", err)
	}
}

func TestEquipmentIssueRollsBackOnBadOrder(t *testing.T) {
	svc, clk, _ := newService()
	seed(svc, clk)
	// Claim equipment but never create/advance a work order to an issuable state.
	if _, res, err := svc.ClaimEquipment("RADIO-1", "o-1", 5); err != nil || !res.Granted {
		t.Fatalf("claim: err=%v res=%+v", err, res)
	}
	// Issuing against a non-existent order must fail AND roll back the lock.
	if _, err := svc.IssueEquipment("RADIO-1", "o-1", "no-such-order"); err == nil {
		t.Fatal("expected issue to fail for missing order")
	}
	eq, _ := svc.Equipment("RADIO-1")
	if eq.Status != domain.EquipAvailable {
		t.Fatalf("expected rollback to available, got %s", eq.Status)
	}
}

func TestEquipmentConcurrentClaimSingleWinner(t *testing.T) {
	svc, clk, _ := newService()
	seed(svc, clk)
	_ = clk
	var wg sync.WaitGroup
	officers := []struct {
		id   string
		prio int
	}{{"A", 2}, {"B", 9}, {"C", 5}, {"D", 7}, {"E", 1}}
	granted := make([]bool, len(officers))
	for i, o := range officers {
		wg.Add(1)
		go func(i int, id string, prio int) {
			defer wg.Done()
			_, res, _ := svc.ClaimEquipment("RADIO-1", id, prio)
			granted[i] = res.Granted
		}(i, o.id, o.prio)
	}
	wg.Wait()
	eq, _ := svc.Equipment("RADIO-1")
	if eq.Priority != 9 {
		t.Fatalf("expected priority 9 to hold, got %d holder=%s", eq.Priority, eq.HolderID)
	}
	if eq.HolderID != "B" {
		t.Fatalf("expected B to hold, got %s", eq.HolderID)
	}
	if len(eq.Queue) != 4 {
		t.Fatalf("expected 4 queued, got %d", len(eq.Queue))
	}
}

func TestReturnAfterShiftRejected(t *testing.T) {
	svc, clk, _ := newService()
	seed(svc, clk)
	start := clk.Now()
	wo, _ := svc.CreateWorkOrder(CreateWorkOrderRequest{
		GridID: "g-general", AssigneeIDs: []string{"o-1"}, PlannedStart: start, ShiftEnd: start.Add(2 * time.Hour),
	})
	svc.Dispatch(wo.ID)
	svc.CheckIn(wo.ID, "o-1", "QR-1")
	svc.ClaimEquipment("RADIO-1", "o-1", 5)
	svc.IssueEquipment("RADIO-1", "o-1", wo.ID)
	// Move past shift end.
	clk.Add(3 * time.Hour)
	if _, err := svc.ReturnEquipment("RADIO-1", "o-1", domain.CondGood); !errors.Is(err, domain.ErrReturnAfterShift) {
		t.Fatalf("expected ErrReturnAfterShift, got %v", err)
	}
}

func TestIncidentReportAndAssign(t *testing.T) {
	svc, clk, _ := newService()
	seed(svc, clk)
	start := clk.Now()
	wo, _ := svc.CreateWorkOrder(CreateWorkOrderRequest{
		GridID: "g-general", AssigneeIDs: []string{"o-1"}, PlannedStart: start, ShiftEnd: start.Add(8 * time.Hour),
	})
	in, err := svc.ReportIncident(ReportIncidentRequest{
		WorkOrderID: wo.ID, Type: domain.IncidentFire, ReporterID: "o-1", IssueID: "issue-1",
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	// Idempotent re-report returns the same incident.
	in2, _ := svc.ReportIncident(ReportIncidentRequest{
		WorkOrderID: wo.ID, Type: domain.IncidentFire, IssueID: "issue-1",
	})
	if in.ID != in2.ID {
		t.Fatal("idempotent report should return same incident")
	}
	// Assign within SLA.
	clk.Add(10 * time.Minute)
	in3, err := svc.AssignIncident(in.ID, "dispatcher-1")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if in3.SLABreached {
		t.Fatal("assign within SLA should not breach")
	}
	clk.Add(5 * time.Minute)
	if _, err := svc.StartHandling(in.ID); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if _, err := svc.CloseIncident(in.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestSubmitTrackDeviation(t *testing.T) {
	svc, clk, _ := newService()
	seed(svc, clk)
	start := clk.Now()
	wo, _ := svc.CreateWorkOrder(CreateWorkOrderRequest{
		GridID: "g-general", AssigneeIDs: []string{"o-1"}, PlannedStart: start, ShiftEnd: start.Add(8 * time.Hour),
	})
	// Off-route track ~500m east of the planned route.
	track, err := svc.SubmitTrack(wo.ID, []domain.TrackPoint{
		{Lat: 38.80, Lng: 100.30, RecordedAt: clk.Now()},
		{Lat: 38.80, Lng: 100.306, RecordedAt: clk.Now().Add(time.Minute)},
	}, false)
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	if !track.Deviation {
		t.Fatal("expected deviation flag for off-route track")
	}
}
