// Package domain contains the core business entities, value objects and state
// machines for the Qilian Hualong patrol dispatch service. Domain types are
// pure: they hold no I/O and are safe to unit test without any infrastructure.
package domain

import "errors"

// ZoneType classifies a patrol grid by protection level.
type ZoneType string

const (
	// ZoneCore marks a core protection zone. Entering a core zone requires a
	// prior report and at least two patrolling officers travelling together.
	ZoneCore ZoneType = "core"
	// ZoneBuffer marks a buffer zone.
	ZoneBuffer ZoneType = "buffer"
	// ZoneGeneral marks a general patrol zone.
	ZoneGeneral ZoneType = "general"
)

// IsValid reports whether z is a recognized zone type.
func (z ZoneType) IsValid() bool {
	switch z {
	case ZoneCore, ZoneBuffer, ZoneGeneral:
		return true
	}
	return false
}

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Grid is a patrol grid with a planned patrol route defined by ordered
// waypoints. The route is used to detect off-route deviations.
type Grid struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	ZoneType ZoneType `json:"zoneType"`
	Route    []Point  `json:"route"`
}

// IsCoreZone reports whether the grid is a core protection zone.
func (g Grid) IsCoreZone() bool { return g.ZoneType == ZoneCore }

// Validate performs basic structural validation of a grid.
func (g Grid) Validate() error {
	if g.ID == "" {
		return errors.New("grid: id is required")
	}
	if g.Name == "" {
		return errors.New("grid: name is required")
	}
	if !g.ZoneType.IsValid() {
		return ErrInvalidZoneType
	}
	if g.IsCoreZone() && len(g.Route) < 2 {
		return ErrCoreZoneRoute
	}
	return nil
}

// Checkpoint is a registration point installed at a grid boundary. Patrol
// officers scan the checkpoint QR code to sign in for a work order.
type Checkpoint struct {
	ID     string `json:"id"`
	GridID string `json:"gridId"`
	Name   string `json:"name"`
	QRCode string `json:"qrCode"`
}

// Validate performs basic structural validation of a checkpoint.
func (c Checkpoint) Validate() error {
	if c.ID == "" {
		return errors.New("checkpoint: id is required")
	}
	if c.GridID == "" {
		return errors.New("checkpoint: gridId is required")
	}
	if c.QRCode == "" {
		return errors.New("checkpoint: qrCode is required")
	}
	return nil
}

// Domain-level sentinel errors shared across entity files.
var (
	ErrInvalidZoneType = errors.New("invalid zone type")
	ErrCoreZoneRoute   = errors.New("core zone grid requires a route with at least 2 waypoints")
)
