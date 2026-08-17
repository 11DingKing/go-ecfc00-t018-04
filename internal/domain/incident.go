package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// IncidentType classifies a reported field incident.
type IncidentType string

const (
	IncidentFire  IncidentType = "fire"
	IncidentPest  IncidentType = "pest" // 病虫害
	IncidentOther IncidentType = "other"
)

// IncidentStatus is the lifecycle state of a reported incident.
type IncidentStatus string

const (
	// IncidentReported: just reported by a patrol officer, awaiting assignment.
	IncidentReported IncidentStatus = "reported"
	// IncidentAssigned: dispatcher assigned a handler within the SLA window.
	IncidentAssigned IncidentStatus = "assigned"
	// IncidentHandling: the assigned handler is on site dealing with it.
	IncidentHandling IncidentStatus = "handling"
	// IncidentClosed: the incident is resolved and tracked to closure.
	IncidentClosed IncidentStatus = "closed"
)

// IsClosed reports whether the incident reached a terminal state.
func (s IncidentStatus) IsClosed() bool { return s == IncidentClosed }

var incidentTransitions = map[IncidentStatus][]IncidentStatus{
	IncidentReported: {IncidentAssigned},
	IncidentAssigned: {IncidentHandling, IncidentClosed},
	IncidentHandling: {IncidentClosed},
	IncidentClosed:   {},
}

// Incident is a fire, pest or other field event reported during a patrol.
type Incident struct {
	ID          string         `json:"id"`
	WorkOrderID string         `json:"workOrderId"`
	Type        IncidentType   `json:"type"`
	Status      IncidentStatus `json:"status"`
	ReporterID  string         `json:"reporterId"`
	HandlerID   string         `json:"handlerId"`
	Description string         `json:"description"`
	Location    Point          `json:"location"`
	ReportedAt  time.Time      `json:"reportedAt"`
	AssignedAt  time.Time      `json:"assignedAt"`
	ClosedAt    time.Time      `json:"closedAt"`
	SLABreached bool           `json:"slaReached"`
	IssueID     string         `json:"issueId"` // links to a unique client-side issue id for idempotency
}

// Assign moves a reported incident to assigned, recording the assignment time.
// SLABreached is set when the assignment happens after the SLA duration; the
// assignment still succeeds so the incident can be handled, but the breach is
// flagged for auditing.
func (in *Incident) Assign(handlerID string, sla time.Duration, now time.Time) error {
	if in.Status != IncidentReported {
		return fmt.Errorf("incident %s: cannot assign from %s: %w", in.ID, in.Status, ErrInvalidTransition)
	}
	if handlerID == "" {
		return ErrHandlerRequired
	}
	in.Status = IncidentAssigned
	in.HandlerID = handlerID
	in.AssignedAt = now
	in.SLABreached = now.Sub(in.ReportedAt) > sla
	return nil
}

// StartHandling moves an assigned incident into the handling state.
func (in *Incident) StartHandling(now time.Time) error {
	if in.Status != IncidentAssigned {
		return fmt.Errorf("incident %s: cannot handle from %s: %w", in.ID, in.Status, ErrInvalidTransition)
	}
	in.Status = IncidentHandling
	return nil
}

// Close resolves the incident.
func (in *Incident) Close(now time.Time) error {
	if !slices.Contains(incidentTransitions[in.Status], IncidentClosed) {
		return fmt.Errorf("incident %s: cannot close from %s: %w", in.ID, in.Status, ErrInvalidTransition)
	}
	in.Status = IncidentClosed
	in.ClosedAt = now
	return nil
}

// IsSLABreached reports whether the reported incident has exceeded the SLA
// duration without being assigned.
func (in Incident) IsSLABreached(sla time.Duration, now time.Time) bool {
	if in.Status != IncidentReported {
		return in.SLABreached
	}
	return now.Sub(in.ReportedAt) > sla
}

// Validate performs structural validation of an incident.
func (in Incident) Validate() error {
	if in.ID == "" {
		return errors.New("incident: id is required")
	}
	if in.WorkOrderID == "" {
		return errors.New("incident: workOrderId is required")
	}
	switch in.Type {
	case IncidentFire, IncidentPest, IncidentOther:
	default:
		return ErrInvalidIncidentType
	}
	if in.IssueID == "" {
		return ErrIssueIDRequired
	}
	return nil
}

// Incident domain errors.
var (
	ErrHandlerRequired     = errors.New("incident: handler is required")
	ErrInvalidIncidentType = errors.New("invalid incident type")
	ErrIssueIDRequired     = errors.New("incident: issueId (idempotency key) is required")
	ErrIncidentNotFound    = errors.New("incident not found")
)
