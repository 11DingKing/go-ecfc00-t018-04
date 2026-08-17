package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"qilian-patrol/internal/app"
	"qilian-patrol/internal/domain"
	"qilian-patrol/internal/store"
	"qilian-patrol/internal/sync"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

func newServer(t *testing.T) (*httptest.Server, *app.Service, *sync.Syncer) {
	t.Helper()
	c := &fakeClock{t: time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)}
	st := store.New(c)
	st.SaveGrid(domain.Grid{ID: "g1", Name: "g", ZoneType: domain.ZoneGeneral, Route: []domain.Point{{Lat: 38.80, Lng: 100.30}, {Lat: 38.805, Lng: 100.305}}})
	st.SaveCheckpoint(domain.Checkpoint{ID: "cp-1", GridID: "g1", QRCode: "QR-1"})
	st.SaveEquipment(domain.Equipment{ID: "eq-1", Code: "RADIO-1", Status: domain.EquipAvailable})
	svc := app.New(st, 30*time.Minute, 200, 24*time.Hour, 2)
	sy := sync.New(svc, time.Second)
	srv := httptest.NewServer(New(svc, sy))
	t.Cleanup(srv.Close)
	return srv, svc, sy
}

func do(t *testing.T, srv *httptest.Server, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestHTTPEndToEndPatrol(t *testing.T) {
	srv, _, _ := newServer(t)
	start := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)
	create := map[string]any{
		"gridId": "g1", "assigneeIds": []string{"o-1"}, "priority": 5,
		"plannedStart": start, "shiftEnd": start.Add(8 * time.Hour), "requestKey": "http-k1",
	}
	resp, body := do(t, srv, "POST", "/api/workorders", create)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status %d body %+v", resp.StatusCode, body)
	}
	id := body["id"].(string)
	for _, step := range []struct {
		path string
		b    any
	}{
		{"/dispatch", nil},
		{"/checkin", map[string]any{"officerId": "o-1", "qrCode": "QR-1"}},
	} {
		if resp, b := do(t, srv, "POST", "/api/workorders/"+id+step.path, step.b); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status %d body %+v", step.path, resp.StatusCode, b)
		}
	}
	// Claim + issue equipment.
	resp, b := do(t, srv, "POST", "/api/equipment/RADIO-1/claim", map[string]any{"officerId": "o-1", "priority": 5})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim status %d body %+v", resp.StatusCode, b)
	}
	claim := b["claim"].(map[string]any)
	if claim["Granted"] != true {
		t.Fatalf("expected claim granted, got %+v", claim)
	}
	if resp, b := do(t, srv, "POST", "/api/equipment/RADIO-1/issue", map[string]any{"officerId": "o-1", "orderId": id}); resp.StatusCode != http.StatusOK {
		t.Fatalf("issue status %d body %+v", resp.StatusCode, b)
	}
	// Start, return, submit track, complete, verify.
	do(t, srv, "POST", "/api/workorders/"+id+"/start", nil)
	do(t, srv, "POST", "/api/equipment/RADIO-1/return", map[string]any{"officerId": "o-1", "condition": "good"})
	track := map[string]any{"workOrderId": id, "points": []map[string]any{
		{"lat": 38.80, "lng": 100.30, "recordedAt": start},
		{"lat": 38.805, "lng": 100.305, "recordedAt": start.Add(time.Minute)},
	}}
	if resp, b := do(t, srv, "POST", "/api/tracks", track); resp.StatusCode != http.StatusCreated {
		t.Fatalf("track status %d body %+v", resp.StatusCode, b)
	}
	do(t, srv, "POST", "/api/workorders/"+id+"/complete", nil)
	if resp, b := do(t, srv, "POST", "/api/workorders/"+id+"/verify", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("verify status %d body %+v", resp.StatusCode, b)
	}
}

func TestHTTPDuplicateWorkOrderConflict(t *testing.T) {
	srv, _, _ := newServer(t)
	start := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)
	create := map[string]any{"gridId": "g1", "assigneeIds": []string{"o-1"}, "plannedStart": start, "shiftEnd": start.Add(8 * time.Hour), "requestKey": "dup-1"}
	do(t, srv, "POST", "/api/workorders", create)
	create["requestKey"] = "dup-2"
	resp, body := do(t, srv, "POST", "/api/workorders", create)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d body %+v", resp.StatusCode, body)
	}
}

func TestHTTPIncidentFlow(t *testing.T) {
	srv, _, _ := newServer(t)
	start := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)
	_, body := do(t, srv, "POST", "/api/workorders", map[string]any{"gridId": "g1", "assigneeIds": []string{"o-1"}, "plannedStart": start, "shiftEnd": start.Add(8 * time.Hour)})
	id := body["id"].(string)
	resp, body := do(t, srv, "POST", "/api/incidents", map[string]any{
		"workOrderId": id, "type": "fire", "reporterId": "o-1", "issueId": "http-inc-1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("report status %d body %+v", resp.StatusCode, body)
	}
	incID := body["id"].(string)
	resp, body = do(t, srv, "POST", "/api/incidents/"+incID+"/assign", map[string]any{"handlerId": "disp-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign status %d body %+v", resp.StatusCode, body)
	}
	if body["slaReached"] != false {
		t.Fatalf("expected slaReached false, got %v", body["slaReached"])
	}
	do(t, srv, "POST", "/api/incidents/"+incID+"/handle", nil)
	if resp, _ := do(t, srv, "POST", "/api/incidents/"+incID+"/close", nil); resp.StatusCode != http.StatusOK {
		t.Fatal("expected close ok")
	}
}

func TestHTTPOfflineSync(t *testing.T) {
	srv, _, sy := newServer(t)
	start := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)
	_, body := do(t, srv, "POST", "/api/workorders", map[string]any{"gridId": "g1", "assigneeIds": []string{"o-1"}, "plannedStart": start, "shiftEnd": start.Add(8 * time.Hour)})
	id := body["id"].(string)
	// Push the work order into existence; offline incident references it.
	offline := map[string]any{
		"records": []map[string]any{
			{
				"localId": "loc-1", "kind": "incident",
				"payload": mustJSON(map[string]any{
					"kind":     "incident",
					"incident": map[string]any{"workOrderId": id, "type": "fire", "reporterId": "o-1", "issueId": "off-http-1"},
				}),
			},
		},
	}
	sy.SetOnline(false)
	resp, body := do(t, srv, "POST", "/api/sync/offline", offline)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("enqueue status %d body %+v", resp.StatusCode, body)
	}
	// While offline the record stays pending.
	resp, body = do(t, srv, "GET", "/api/sync/status", nil)
	stats := body["stats"].(map[string]any)
	if int(stats["Pending"].(float64)) != 1 {
		t.Fatalf("expected 1 pending, got %+v", stats)
	}
	// Restore connectivity: reconcile runs and the record syncs.
	do(t, srv, "POST", "/api/sync/online", map[string]any{"online": true})
	resp, body = do(t, srv, "GET", "/api/sync/status", nil)
	stats = body["stats"].(map[string]any)
	if int(stats["Synced"].(float64)) != 1 {
		t.Fatalf("expected 1 synced, got %+v", stats)
	}
}

func TestHTTPHealthz(t *testing.T) {
	srv, _, _ := newServer(t)
	resp, body := do(t, srv, "GET", "/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status %d", resp.StatusCode)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected ok, got %v", body["status"])
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
