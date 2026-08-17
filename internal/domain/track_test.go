package domain

import (
	"math"
	"testing"
	"time"
)

func TestHaversineKnownDistance(t *testing.T) {
	// ~1 degree of latitude near the equator is ~111km.
	d := HaversineMeters(Point{Lat: 0, Lng: 0}, Point{Lat: 1, Lng: 0})
	if math.Abs(d-111195) > 500 {
		t.Fatalf("expected ~111195m, got %.1f", d)
	}
}

func TestTrackDeviationWarning(t *testing.T) {
	route := []Point{{Lat: 38.9000, Lng: 100.4000}, {Lat: 38.9050, Lng: 100.4000}}
	// On-route point: no deviation.
	track := &PatrolTrack{Points: []TrackPoint{
		{Lat: 38.9025, Lng: 100.4000, RecordedAt: time.Now()},
		{Lat: 38.9040, Lng: 100.4000, RecordedAt: time.Now().Add(time.Second)},
	}}
	track.Evaluate(route, 200)
	if track.Deviation {
		t.Fatalf("on-route should not deviate, maxDev=%.1f", track.MaxDevM)
	}
	// Off-route point ~500m east: should deviate beyond 200m.
	off := &PatrolTrack{Points: []TrackPoint{
		{Lat: 38.9020, Lng: 100.4000, RecordedAt: time.Now()},
		// ~0.006 deg lng at lat ~38.9 is roughly ~525m.
		{Lat: 38.9020, Lng: 100.4060, RecordedAt: time.Now().Add(time.Second)},
	}}
	off.Evaluate(route, 200)
	if !off.Deviation {
		t.Fatalf("off-route should deviate, maxDev=%.1f", off.MaxDevM)
	}
	if off.MaxDevM < 200 {
		t.Fatalf("maxDev should exceed threshold, got %.1f", off.MaxDevM)
	}
}

func TestTrackEvaluateEmptyRoute(t *testing.T) {
	track := &PatrolTrack{Points: []TrackPoint{{Lat: 1, Lng: 1, RecordedAt: time.Now()}}}
	track.Evaluate(nil, 200)
	if track.Deviation {
		t.Fatal("empty route should not flag deviation")
	}
}

func TestTrackValidation(t *testing.T) {
	if err := (PatrolTrack{WorkOrderID: "wo"}).Validate(); err == nil {
		t.Fatal("expected validation error for too few points")
	}
	if err := (PatrolTrack{Points: []TrackPoint{{Lat: 1, Lng: 1}, {Lat: 2, Lng: 2}}}).Validate(); err == nil {
		t.Fatal("expected validation error for missing workOrderId")
	}
}
