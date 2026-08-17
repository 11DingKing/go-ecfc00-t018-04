package domain

import (
	"sync"
	"testing"
	"time"
)

func TestEquipmentClaimPriorityPreempts(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	e := &Equipment{ID: "eq", Code: "R1", Status: EquipAvailable}
	// Low-priority officer locks first.
	r1 := e.Claim("officer-low", 1, now)
	if !r1.Granted {
		t.Fatalf("expected first claim granted, got %+v", r1)
	}
	// Higher-priority officer preempts the unissued lock.
	r2 := e.Claim("officer-high", 9, now.Add(time.Second))
	if !r2.Granted {
		t.Fatalf("expected high priority to preempt, got %+v", r2)
	}
	if e.HolderID != "officer-high" {
		t.Fatalf("expected officer-high to hold, got %s", e.HolderID)
	}
	// Displaced low-priority officer should be queued.
	if len(e.Queue) != 1 || e.Queue[0].OfficerID != "officer-low" {
		t.Fatalf("expected low-priority queued, got %+v", e.Queue)
	}
}

func TestEquipmentClaimLoserQueued(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	e := &Equipment{ID: "eq", Code: "R1", Status: EquipAvailable}
	e.Claim("officer-high", 9, now)
	// Lower-priority claim against an issued item is queued, not granted.
	e.Status = EquipIssued
	r := e.Claim("officer-low", 1, now.Add(time.Second))
	if r.Granted {
		t.Fatal("expected claim to be queued, not granted")
	}
	if r.Position != 1 {
		t.Fatalf("expected queue position 1, got %d", r.Position)
	}
}

func TestEquipmentIssueReturnFlow(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	e := &Equipment{ID: "eq", Code: "R1", Status: EquipAvailable}
	e.Claim("o1", 5, now)
	if err := e.Issue("o1", "wo-1", now); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if e.Status != EquipIssued || e.OrderID != "wo-1" {
		t.Fatalf("unexpected state: %+v", e)
	}
	// Wrong officer cannot return.
	if err := e.Return("o2", CondGood, now); err == nil {
		t.Fatal("expected error returning from non-holder")
	}
	if err := e.Return("o1", CondGood, now); err != nil {
		t.Fatalf("return: %v", err)
	}
	if e.Status != EquipAvailable {
		t.Fatalf("expected available after return, got %s", e.Status)
	}
}

func TestEquipmentReleaseLockRollsBackAndPromotes(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	e := &Equipment{ID: "eq", Code: "R1", Status: EquipAvailable}
	e.Claim("high", 9, now)
	e.Claim("low", 1, now.Add(time.Second)) // queued
	if len(e.Queue) != 1 {
		t.Fatalf("expected 1 queued, got %d", len(e.Queue))
	}
	// Rollback the held lock; the queued claim should be promoted.
	e.ReleaseLock(now.Add(2 * time.Second))
	if e.Status != EquipLocked || e.HolderID != "low" {
		t.Fatalf("expected promoted to low, got status=%s holder=%s", e.Status, e.HolderID)
	}
	if len(e.Queue) != 0 {
		t.Fatalf("expected empty queue after promotion, got %d", len(e.Queue))
	}
}

func TestEquipmentConcurrentClaimsHighestPriorityWins(t *testing.T) {
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	// Several officers concurrently claim the same available item with mixed
	// reporting priorities. Regardless of goroutine scheduling, the final holder
	// must be the highest-priority claim and every other claim must be queued,
	// matching "按报备优先级锁定，仅一人成功，另一人保留排队位次".
	e := &Equipment{ID: "eq", Code: "R1", Status: EquipAvailable}
	var mu sync.Mutex
	var wg sync.WaitGroup
	type req struct {
		officer string
		prio    int
	}
	reqs := []req{{"o3", 3}, {"o7", 7}, {"o5", 5}, {"o9", 9}, {"o1", 1}}
	for i, r := range reqs {
		wg.Add(1)
		go func(i int, r req) {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			e.Claim(r.officer, r.prio, now.Add(time.Duration(i)*time.Nanosecond))
		}(i, r)
	}
	wg.Wait()
	if e.Priority != 9 {
		t.Fatalf("expected highest priority 9 to hold, got %d (holder=%s)", e.Priority, e.HolderID)
	}
	if e.HolderID != "o9" {
		t.Fatalf("expected o9 to hold, got %s", e.HolderID)
	}
	if len(e.Queue) != 4 {
		t.Fatalf("expected 4 queued claims, got %d (%+v)", len(e.Queue), e.Queue)
	}
}
