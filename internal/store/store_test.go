package store

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"qilian-patrol/internal/domain"
)

// fakeClock is a controllable Clock for deterministic tests.
type fakeClock struct{ t atomic.Int64 }

func (f *fakeClock) Now() time.Time   { return time.Unix(f.t.Load(), 0) }
func (f *fakeClock) Set(ts time.Time) { f.t.Store(ts.Unix()) }
func (f *fakeClock) Add(d time.Duration) time.Time {
	f.t.Add(int64(d.Seconds()))
	return f.Now()
}

func newFakeStore() (*Store, *fakeClock) {
	c := &fakeClock{}
	c.Set(time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC))
	return New(c), c
}

func TestCreateWorkOrderDuplicateWindow(t *testing.T) {
	s, clk := newFakeStore()
	w1 := domain.WorkOrder{ID: "wo-1", GridID: "g1", Status: domain.StatusDraft}
	if _, created, err := s.CreateWorkOrder(w1, "k1", 24*time.Hour); err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	// Second order for the same grid within 24h must be rejected.
	w2 := domain.WorkOrder{ID: "wo-2", GridID: "g1", Status: domain.StatusDraft}
	if _, _, err := s.CreateWorkOrder(w2, "k2", 24*time.Hour); !errors.Is(err, domain.ErrDuplicateOrder) {
		t.Fatalf("expected ErrDuplicateOrder, got %v", err)
	}
	// After 25 hours the window frees up.
	clk.Add(25 * time.Hour)
	w3 := domain.WorkOrder{ID: "wo-3", GridID: "g1", Status: domain.StatusDraft}
	if _, created, err := s.CreateWorkOrder(w3, "k3", 24*time.Hour); err != nil || !created {
		t.Fatalf("post-window create: created=%v err=%v", created, err)
	}
}

func TestCreateWorkOrderIdempotency(t *testing.T) {
	s, _ := newFakeStore()
	w := domain.WorkOrder{ID: "wo-1", GridID: "g1", Status: domain.StatusDraft}
	s.CreateWorkOrder(w, "req-1", 24*time.Hour)
	// Same request key returns the existing order, does not create a new one.
	got, created, err := s.CreateWorkOrder(domain.WorkOrder{ID: "should-be-ignored", GridID: "g1"}, "req-1", 24*time.Hour)
	if err != nil || created {
		t.Fatalf("idempotent create should not recreate: created=%v err=%v", created, err)
	}
	if got.ID != "wo-1" {
		t.Fatalf("expected wo-1, got %s", got.ID)
	}
}

func TestPutIncidentAtomicIdempotent(t *testing.T) {
	s, _ := newFakeStore()
	in := domain.Incident{ID: "inc-1", WorkOrderID: "wo", Type: domain.IncidentFire, IssueID: "issue-1", Status: domain.IncidentReported}
	got1, created1, err := s.PutIncidentAtomic(in)
	if err != nil || !created1 {
		t.Fatalf("first put: created=%v err=%v", created1, err)
	}
	// Re-submit with a different ID but same IssueID: must return the original.
	again := domain.Incident{ID: "inc-2", WorkOrderID: "wo", Type: domain.IncidentPest, IssueID: "issue-1"}
	got2, created2, err := s.PutIncidentAtomic(again)
	if err != nil || created2 {
		t.Fatalf("second put should be idempotent: created=%v err=%v", created2, err)
	}
	if got2.ID != "inc-1" {
		t.Fatalf("expected inc-1, got %s", got2.ID)
	}
	if got1.ID != got2.ID {
		t.Fatal("idempotent get returned different incidents")
	}
}

func TestClaimEquipmentContention(t *testing.T) {
	s, _ := newFakeStore()
	s.SaveEquipment(domain.Equipment{ID: "eq-1", Code: "R1", Status: domain.EquipAvailable})
	var wg sync.WaitGroup
	priorities := []int{3, 7, 5, 9, 1}
	results := make([]domain.ClaimResult, len(priorities))
	for i, p := range priorities {
		wg.Add(1)
		go func(i, p int) {
			defer wg.Done()
			_, res, _ := s.ClaimEquipment("eq-1", "officer-"+string(rune('A'+i)), p)
			results[i] = res
		}(i, p)
	}
	wg.Wait()
	eq, _ := s.Equipment("eq-1")
	if eq.Priority != 9 {
		t.Fatalf("expected priority 9 to hold, got %d holder=%s", eq.Priority, eq.HolderID)
	}
	if len(eq.Queue) != 4 {
		t.Fatalf("expected 4 queued, got %d", len(eq.Queue))
	}
}

func TestOfflineRecordIdempotent(t *testing.T) {
	s, _ := newFakeStore()
	r := OfflineRecord{LocalID: "loc-1", Kind: OfflineKindIncident, Payload: []byte("{}")}
	if _, created := s.EnqueueOffline(r); !created {
		t.Fatal("expected first enqueue to create")
	}
	if _, created := s.EnqueueOffline(r); created {
		t.Fatal("expected second enqueue to be idempotent (no create)")
	}
	pending := s.PendingOffline()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	// Mark syncing then synced; receipt is stable on re-mark.
	if _, ok := s.MarkSyncing("loc-1"); !ok {
		t.Fatal("expected mark syncing")
	}
	s.MarkSynced("loc-1", "rec-1")
	rec, _ := s.OfflineReceipt("rec-1")
	if rec.Status != OfflineSynced {
		t.Fatalf("expected synced, got %s", rec.Status)
	}
	// Mark syncing again on a synced record should not flip it back.
	if _, ok := s.MarkSyncing("loc-1"); ok {
		t.Fatal("synced record should not be re-syncable")
	}
}
