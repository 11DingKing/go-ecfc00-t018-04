package sync

import (
	"encoding/json"
	"testing"
	"time"

	"qilian-patrol/internal/app"
	"qilian-patrol/internal/domain"
	"qilian-patrol/internal/store"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time                { return f.t }
func (f *fakeClock) Add(d time.Duration) time.Time { f.t = f.t.Add(d); return f.t }

func newSyncer() (*Syncer, *app.Service, *store.Store, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)}
	st := store.New(c)
	st.SaveGrid(domain.Grid{ID: "g1", Name: "g", ZoneType: domain.ZoneGeneral, Route: []domain.Point{{Lat: 38.80, Lng: 100.30}, {Lat: 38.805, Lng: 100.305}}})
	st.SaveCheckpoint(domain.Checkpoint{ID: "cp-1", GridID: "g1", QRCode: "QR-1"})
	st.SaveEquipment(domain.Equipment{ID: "eq-1", Code: "R1", Status: domain.EquipAvailable})
	svc := app.New(st, 30*time.Minute, 200, 24*time.Hour, 2)
	// Seed a valid work order to attach incidents and tracks to.
	wo, _ := svc.CreateWorkOrder(app.CreateWorkOrderRequest{
		GridID: "g1", AssigneeIDs: []string{"o-1"}, PlannedStart: c.Now(), ShiftEnd: c.Now().Add(8 * time.Hour),
	})
	svc.Dispatch(wo.ID)
	_ = wo
	return New(svc, time.Second), svc, st, c
}

func TestOfflineRecoveryReplaysRecords(t *testing.T) {
	sy, svc, st, clk := newSyncer()
	wo, _ := svc.WorkOrder(svc.WorkOrders()[0].ID)
	// Cache an offline incident and track while "offline".
	payloadInc, _ := json.Marshal(app.OfflinePayload{
		Kind:     string(store.OfflineKindIncident),
		Incident: &app.ReportIncidentRequest{WorkOrderID: wo.ID, Type: domain.IncidentFire, ReporterID: "o-1", IssueID: "off-issue-1"},
	})
	payloadTrack, _ := json.Marshal(app.OfflinePayload{
		Kind:  string(store.OfflineKindTrack),
		Track: &app.OfflineTrack{WorkOrderID: wo.ID, Points: []domain.TrackPoint{{Lat: 38.80, Lng: 100.30, RecordedAt: clk.Now()}, {Lat: 38.805, Lng: 100.305, RecordedAt: clk.Now().Add(time.Minute)}}},
	})
	svc.EnqueueOffline(store.OfflineRecord{LocalID: "loc-inc", Kind: store.OfflineKindIncident, Payload: payloadInc})
	svc.EnqueueOffline(store.OfflineRecord{LocalID: "loc-track", Kind: store.OfflineKindTrack, Payload: payloadTrack})

	// While offline, reconcile should not apply anything.
	sy.SetOnline(false)
	synced, failed := sy.reconcileOffline()
	if synced != 0 || failed != 0 {
		t.Fatalf("offline reconcile should be no-op, got synced=%d failed=%d", synced, failed)
	}
	if st.OfflineStats().Pending != 2 {
		t.Fatalf("expected 2 pending, got %d", st.OfflineStats().Pending)
	}

	// Restore connectivity and reconcile: both records sync with receipts.
	sy.SetOnline(true)
	synced, failed = sy.reconcileOffline()
	if synced != 2 || failed != 0 {
		t.Fatalf("expected 2 synced 0 failed, got synced=%d failed=%d", synced, failed)
	}
	if st.OfflineStats().Synced != 2 {
		t.Fatalf("expected 2 synced in stats, got %d", st.OfflineStats().Synced)
	}
	// Each record has a receipt.
	rec1, ok := st.OfflineRecord("loc-inc")
	if !ok || rec1.ReceiptID == "" {
		t.Fatalf("expected receipt for loc-inc, got %+v", rec1)
	}
	rec2, ok := st.OfflineRecord("loc-track")
	if !ok || rec2.ReceiptID == "" {
		t.Fatalf("expected receipt for loc-track, got %+v", rec2)
	}
}

func TestOfflineRecoveryIdempotentReplay(t *testing.T) {
	sy, svc, _, _ := newSyncer()
	wo, _ := svc.WorkOrder(svc.WorkOrders()[0].ID)
	payload, _ := json.Marshal(app.OfflinePayload{
		Kind:     string(store.OfflineKindIncident),
		Incident: &app.ReportIncidentRequest{WorkOrderID: wo.ID, Type: domain.IncidentPest, ReporterID: "o-1", IssueID: "off-issue-2"},
	})
	svc.EnqueueOffline(store.OfflineRecord{LocalID: "loc-2", Kind: store.OfflineKindIncident, Payload: payload})
	sy.SetOnline(true)
	sy.reconcileOffline()
	before := len(svc.Incidents())
	// Re-running reconcile must not create a second incident: the synced record
	// is skipped, and even re-applying the same IssueID is idempotent.
	sy.reconcileOffline()
	after := len(svc.Incidents())
	if before != after {
		t.Fatalf("idempotent replay created duplicates: before=%d after=%d", before, after)
	}
	if sy.SyncedCount() != 1 {
		t.Fatalf("expected synced count 1, got %d", sy.SyncedCount())
	}
}

func TestSLAMonitorFlagsBreached(t *testing.T) {
	sy, svc, _, clk := newSyncer()
	wo, _ := svc.WorkOrder(svc.WorkOrders()[0].ID)
	in, _ := svc.ReportIncident(app.ReportIncidentRequest{WorkOrderID: wo.ID, Type: domain.IncidentFire, ReporterID: "o-1", IssueID: "sla-issue-1"})
	// Within SLA: no flag.
	sy.monitorSLA()
	if sy.SLAFlagged() != 0 {
		t.Fatal("expected no SLA flag within window")
	}
	// Advance 31 minutes: SLA breached.
	clk.Add(31 * time.Minute)
	n := sy.monitorSLA()
	if n != 1 {
		t.Fatalf("expected 1 newly flagged, got %d", n)
	}
	got, _ := svc.Incident(in.ID)
	if !got.SLABreached {
		t.Fatal("expected incident flagged as SLA breached")
	}
	// A second monitor pass must not re-flag.
	if sy.monitorSLA() != 0 {
		t.Fatal("expected no additional flags on second pass")
	}
}

func TestTickOnceCombinesBoth(t *testing.T) {
	sy, svc, _, clk := newSyncer()
	wo, _ := svc.WorkOrder(svc.WorkOrders()[0].ID)
	payload, _ := json.Marshal(app.OfflinePayload{
		Kind:     string(store.OfflineKindIncident),
		Incident: &app.ReportIncidentRequest{WorkOrderID: wo.ID, Type: domain.IncidentOther, ReporterID: "o-1", IssueID: "tick-issue-1"},
	})
	svc.EnqueueOffline(store.OfflineRecord{LocalID: "loc-tick", Kind: store.OfflineKindIncident, Payload: payload})
	clk.Add(31 * time.Minute)
	// No reported incident to breach here, but the offline record should sync.
	sla, synced, failed := sy.TickOnce()
	_ = sla
	if synced != 1 || failed != 0 {
		t.Fatalf("expected 1 synced, got synced=%d failed=%d", synced, failed)
	}
}
