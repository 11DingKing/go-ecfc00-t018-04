package domain

import (
	"errors"
	"math"
	"sort"
	"time"
)

// TrackPoint is a timestamped patrol GPS fix.
type TrackPoint struct {
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	RecordedAt time.Time `json:"recordedAt"`
}

// PatrolTrack is the recorded GPS trace for a work order.
type PatrolTrack struct {
	WorkOrderID string       `json:"workOrderId"`
	Points      []TrackPoint `json:"points"`
	SubmittedAt time.Time    `json:"submittedAt"`
	Deviation   bool         `json:"deviation"` // > threshold from planned route
	MaxDevM     float64      `json:"maxDevMeters"`
	Offline     bool         `json:"offline"` // submitted via offline cache
}

// earthRadiusM is the mean Earth radius in metres used by Haversine.
const earthRadiusM = 6371000.0

func toRad(deg float64) float64 { return deg * math.Pi / 180 }

// HaversineMeters returns the great-circle distance in metres between two
// coordinates.
func HaversineMeters(a, b Point) float64 {
	dLat := toRad(b.Lat - a.Lat)
	dLng := toRad(b.Lng - a.Lng)
	lat1 := toRad(a.Lat)
	lat2 := toRad(b.Lat)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	c := 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
	return earthRadiusM * c
}

// project converts a lat/lng to a local planar (x, y) coordinate in metres
// using an equirectangular projection centred on the segment mid-latitude. This
// is accurate enough for sub-kilometre deviation checks.
func project(p Point, lat0Rad float64) (x, y float64) {
	x = toRad(p.Lng) * math.Cos(lat0Rad) * earthRadiusM
	y = toRad(p.Lat) * earthRadiusM
	return x, y
}

// pointToSegmentMeters returns the distance from p to the segment a-b in metres.
func pointToSegmentMeters(p, a, b Point) float64 {
	if a.Lat == b.Lat && a.Lng == b.Lng {
		return HaversineMeters(p, a)
	}
	lat0 := toRad((a.Lat + b.Lat) / 2)
	ax, ay := project(a, lat0)
	bx, by := project(b, lat0)
	px, py := project(p, lat0)
	dx := bx - ax
	dy := by - ay
	segLen2 := dx*dx + dy*dy
	t := ((px-ax)*dx + (py-ay)*dy) / segLen2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	cx := ax + t*dx
	cy := ay + t*dy
	return math.Sqrt((px-cx)*(px-cx) + (py-cy)*(py-cy))
}

// distanceToRouteMeters returns the minimum distance from p to the nearest
// segment of the planned route polyline.
func distanceToRouteMeters(p Point, route []Point) float64 {
	if len(route) == 0 {
		return math.Inf(1)
	}
	if len(route) == 1 {
		return HaversineMeters(p, route[0])
	}
	min := math.Inf(1)
	for i := 0; i+1 < len(route); i++ {
		d := pointToSegmentMeters(p, route[i], route[i+1])
		if d < min {
			min = d
		}
	}
	return min
}

// Evaluate computes the maximum off-route deviation for the track against the
// given route and flags a deviation warning when it exceeds the threshold.
func (t *PatrolTrack) Evaluate(route []Point, thresholdM float64) {
	if len(t.Points) == 0 || len(route) == 0 {
		return
	}
	ordered := make([]TrackPoint, len(t.Points))
	copy(ordered, t.Points)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].RecordedAt.Before(ordered[j].RecordedAt) })
	maxDev := 0.0
	for _, tp := range ordered {
		p := Point{Lat: tp.Lat, Lng: tp.Lng}
		d := distanceToRouteMeters(p, route)
		if d > maxDev {
			maxDev = d
		}
	}
	t.MaxDevM = maxDev
	t.Deviation = maxDev > thresholdM
}

// Validate performs structural validation of a track.
func (t PatrolTrack) Validate() error {
	if t.WorkOrderID == "" {
		return errors.New("track: workOrderId is required")
	}
	if len(t.Points) < 2 {
		return ErrTrackTooShort
	}
	return nil
}

// Track domain errors.
var (
	ErrTrackTooShort = errors.New("track must contain at least 2 points")
	ErrTrackNotFound = errors.New("track not found")
)
