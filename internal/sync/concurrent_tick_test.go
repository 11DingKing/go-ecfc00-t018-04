package sync

import (
	"encoding/json"
	"fmt"
	stdsync "sync"
	"testing"

	"qilian-patrol/internal/app"
	"qilian-patrol/internal/domain"
	"qilian-patrol/internal/store"
)

// TestConcurrentTicksReportEverySyncedRecord exercises the supported concurrent
// usage of the background worker: the periodic loop and the HTTP handlers both
// drive reconcile passes while clients read the exposed sync counters. Every
// cached record must end up synced, and the reported synced count must account
// for all of them.
func TestConcurrentTicksReportEverySyncedRecord(t *testing.T) {
	sy, svc, st, clk := newSyncer()
	wo := svc.WorkOrders()[0]

	const records = 240
	for i := 0; i < records; i++ {
		payload, err := json.Marshal(app.OfflinePayload{
			Kind: string(store.OfflineKindIncident),
			Incident: &app.ReportIncidentRequest{
				WorkOrderID: wo.ID,
				Type:        domain.IncidentPest,
				ReporterID:  "o-1",
				IssueID:     fmt.Sprintf("conc-issue-%03d", i),
			},
		})
		if err != nil {
			t.Fatalf("marshal payload %d: %v", i, err)
		}
		svc.EnqueueOffline(store.OfflineRecord{
			LocalID:   fmt.Sprintf("loc-conc-%03d", i),
			Kind:      store.OfflineKindIncident,
			Payload:   payload,
			CreatedAt: clk.Now(),
		})
	}
	sy.SetOnline(true)

	var workers stdsync.WaitGroup
	for w := 0; w < 6; w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 30; i++ {
				sy.TickOnce()
			}
		}()
	}

	var reader stdsync.WaitGroup
	stop := make(chan struct{})
	reader.Add(1)
	go func() {
		defer reader.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = sy.SyncedCount()
			}
		}
	}()

	workers.Wait()
	close(stop)
	reader.Wait()

	stats := st.OfflineStats()
	if stats.Synced != records {
		t.Fatalf("synced records = %d, want %d (pending=%d failed=%d)", stats.Synced, records, stats.Pending, stats.Failed)
	}
	if got := sy.SyncedCount(); got < int64(records) {
		t.Fatalf("SyncedCount() = %d, want at least %d reconciled records to be counted", got, records)
	}
}
