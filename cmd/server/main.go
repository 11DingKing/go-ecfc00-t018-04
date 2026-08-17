// Command patrol runs the Qilian Hualong patrol dispatch HTTP service.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"qilian-patrol/internal/app"
	"qilian-patrol/internal/config"
	"qilian-patrol/internal/domain"
	"qilian-patrol/internal/httpapi"
	"qilian-patrol/internal/store"
	"qilian-patrol/internal/sync"
)

func main() {
	configPath := flag.String("config", "config.json", "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	st := store.New(nil)
	if err := seed(st); err != nil {
		log.Fatalf("seed: %v", err)
	}

	svc := app.New(st, cfg.SLADuration, cfg.DeviationThresholdM, cfg.GridReuseWindow, cfg.CoreZoneMinAssignees)
	sy := sync.New(svc, cfg.SyncInterval)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go sy.Run(ctx)

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           httpapi.New(svc, sy),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("patrol dispatch service listening on %s", cfg.Listen)
	go func() {
		<-ctx.Done()
		shutCtx, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShut()
		_ = server.Shutdown(shutCtx)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
	log.Printf("service stopped")
}

// seed installs a small demonstration dataset so the service is immediately
// usable: one general grid, one core grid with a patrol route, a checkpoint
// per grid, and two radios.
func seed(st *store.Store) error {
	grids := []domain.Grid{
		{
			ID: "grid-g1", Name: "华隆大峡谷巡护网格", ZoneType: domain.ZoneGeneral,
			Route: []domain.Point{{Lat: 38.8000, Lng: 100.3000}, {Lat: 38.8050, Lng: 100.3050}, {Lat: 38.8100, Lng: 100.3100}},
		},
		{
			ID: "grid-c1", Name: "祁连核心区冷龙岭网格", ZoneType: domain.ZoneCore,
			Route: []domain.Point{{Lat: 38.9000, Lng: 100.4000}, {Lat: 38.9050, Lng: 100.4050}, {Lat: 38.9100, Lng: 100.4100}},
		},
	}
	for _, g := range grids {
		st.SaveGrid(g)
	}
	st.SaveCheckpoint(domain.Checkpoint{ID: "cp-g1", GridID: "grid-g1", Name: "大峡谷卡口", QRCode: "QR-G1"})
	st.SaveCheckpoint(domain.Checkpoint{ID: "cp-c1", GridID: "grid-c1", Name: "冷龙岭卡口", QRCode: "QR-C1"})
	for _, e := range []domain.Equipment{
		{ID: "eq-1", Code: "RADIO-001", Name: "对讲机", Status: domain.EquipAvailable},
		{ID: "eq-2", Code: "RADIO-002", Name: "对讲机", Status: domain.EquipAvailable},
		{ID: "eq-3", Code: "GPS-001", Name: "GPS终端", Status: domain.EquipAvailable},
	} {
		st.SaveEquipment(e)
	}
	return nil
}
