package app

import (
	"encoding/json"
	"errors"
	"fmt"

	"qilian-patrol/internal/domain"
	"qilian-patrol/internal/store"
)

// ReportIncidentRequest captures a field incident report.
type ReportIncidentRequest struct {
	WorkOrderID string
	Type        domain.IncidentType
	ReporterID  string
	Description string
	Location    domain.Point
	IssueID     string // client idempotency key
	Offline     bool
}

// ReportIncident creates a reported incident. Re-reporting with the same IssueID
// returns the original incident, making the operation idempotent for offline
// re-submission.
func (s *Service) ReportIncident(req ReportIncidentRequest) (domain.Incident, error) {
	if req.IssueID == "" {
		req.IssueID = newID("issue")
	}
	if existing, ok := s.store.IncidentByIssue(req.IssueID); ok {
		return existing, nil
	}
	if _, ok := s.store.WorkOrder(req.WorkOrderID); !ok {
		return domain.Incident{}, fmt.Errorf("work order %s: %w", req.WorkOrderID, ErrOrderNotFound)
	}
	in := domain.Incident{
		ID:          newID("inc"),
		WorkOrderID: req.WorkOrderID,
		Type:        req.Type,
		Status:      domain.IncidentReported,
		ReporterID:  req.ReporterID,
		Description: req.Description,
		Location:    req.Location,
		IssueID:     req.IssueID,
		ReportedAt:  s.now(),
	}
	if err := in.Validate(); err != nil {
		return domain.Incident{}, err
	}
	stored, _, err := s.store.PutIncidentAtomic(in)
	if err != nil {
		return domain.Incident{}, err
	}
	return stored, nil
}

// AssignIncident assigns a handler to a reported incident within the SLA. The
// assignment still succeeds past the SLA but the breach is flagged for audit.
func (s *Service) AssignIncident(incidentID, handlerID string) (domain.Incident, error) {
	return s.store.MutateIncident(incidentID, func(in *domain.Incident, rx *store.LockedView) error {
		return in.Assign(handlerID, s.sla, s.now())
	})
}

// StartHandling moves an assigned incident into the handling state.
func (s *Service) StartHandling(incidentID string) (domain.Incident, error) {
	return s.store.MutateIncident(incidentID, func(in *domain.Incident, rx *store.LockedView) error {
		return in.StartHandling(s.now())
	})
}

// CloseIncident resolves an incident.
func (s *Service) CloseIncident(incidentID string) (domain.Incident, error) {
	return s.store.MutateIncident(incidentID, func(in *domain.Incident, rx *store.LockedView) error {
		return in.Close(s.now())
	})
}

// Incident retrieves an incident.
func (s *Service) Incident(id string) (domain.Incident, bool) { return s.store.Incident(id) }

// Incidents lists all incidents.
func (s *Service) Incidents() []domain.Incident { return s.store.Incidents() }

// FlagSLABreaches marks reported incidents that have exceeded the response SLA.
// It returns the number of newly-flagged incidents. Called by the background
// monitor.
func (s *Service) FlagSLABreaches() int {
	now := s.now()
	flagged := 0
	for _, in := range s.store.OpenIncidents() {
		if in.Status != domain.IncidentReported {
			continue
		}
		if !in.IsSLABreached(s.sla, now) {
			continue
		}
		_, _ = s.store.MutateIncident(in.ID, func(rec *domain.Incident, rx *store.LockedView) error {
			if rec.Status == domain.IncidentReported && !rec.SLABreached {
				rec.SLABreached = true
				flagged++
			}
			return nil
		})
	}
	return flagged
}

// SubmitTrack submits a patrol track for a work order, evaluating off-route
// deviation against the grid's planned route. A deviation beyond the threshold
// raises an automatic warning flag.
func (s *Service) SubmitTrack(workOrderID string, points []domain.TrackPoint, offline bool) (domain.PatrolTrack, error) {
	w, ok := s.store.WorkOrder(workOrderID)
	if !ok {
		return domain.PatrolTrack{}, fmt.Errorf("work order %s: %w", workOrderID, ErrOrderNotFound)
	}
	grid, ok := s.store.Grid(w.GridID)
	if !ok {
		return domain.PatrolTrack{}, ErrGridNotFound
	}
	t := domain.PatrolTrack{
		WorkOrderID: workOrderID,
		Points:      append([]domain.TrackPoint(nil), points...),
		SubmittedAt: s.now(),
		Offline:     offline,
	}
	if err := t.Validate(); err != nil {
		return domain.PatrolTrack{}, err
	}
	t.Evaluate(grid.Route, s.deviationM)
	s.store.SaveTrack(t)
	return t, nil
}

// Track retrieves a patrol track.
func (s *Service) Track(workOrderID string) (domain.PatrolTrack, bool) {
	return s.store.Track(workOrderID)
}

// --- Offline reconciliation ---

// OfflinePayload is the JSON envelope stored inside an OfflineRecord.
type OfflinePayload struct {
	Kind       string                 `json:"kind"`
	Incident   *ReportIncidentRequest `json:"incident,omitempty"`
	Track      *OfflineTrack          `json:"track,omitempty"`
	PatrolNote string                 `json:"patrolNote,omitempty"`
}

// OfflineTrack is the track payload for offline submission.
type OfflineTrack struct {
	WorkOrderID string              `json:"workOrderId"`
	Points      []domain.TrackPoint `json:"points"`
}

// ApplyOfflineRecord applies one cached record to the live store and returns a
// receipt id. Applying the same record twice yields the same receipt (idempotent
// reconciliation).
func (s *Service) ApplyOfflineRecord(r store.OfflineRecord) (string, error) {
	var p OfflinePayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return "", fmt.Errorf("unmarshal offline payload: %w", err)
	}
	switch p.Kind {
	case string(store.OfflineKindIncident):
		if p.Incident == nil {
			return "", errors.New("offline incident payload missing")
		}
		p.Incident.Offline = true
		in, err := s.ReportIncident(*p.Incident)
		if err != nil {
			return "", err
		}
		return "rec:" + in.ID, nil
	case string(store.OfflineKindTrack):
		if p.Track == nil {
			return "", errors.New("offline track payload missing")
		}
		t, err := s.SubmitTrack(p.Track.WorkOrderID, p.Track.Points, true)
		if err != nil {
			return "", err
		}
		return "rec:" + t.WorkOrderID, nil
	case string(store.OfflineKindPatrol):
		return "rec:" + r.LocalID, nil
	default:
		return "", fmt.Errorf("unknown offline kind %q", p.Kind)
	}
}

// OfflineStats exposes offline cache statistics.
func (s *Service) OfflineStats() store.OfflineStats { return s.store.OfflineStats() }

// EnqueueOffline stores a pending offline record.
func (s *Service) EnqueueOffline(r store.OfflineRecord) (store.OfflineRecord, bool) {
	return s.store.EnqueueOffline(r)
}

// PendingOffline lists records awaiting sync.
func (s *Service) PendingOffline() []store.OfflineRecord { return s.store.PendingOffline() }

// MarkSynced confirms an offline record with a receipt.
func (s *Service) MarkSynced(localID, receiptID string) { s.store.MarkSynced(localID, receiptID) }

// MarkFailed records a sync failure for retry.
func (s *Service) MarkFailed(localID string, err error) {
	if err == nil {
		return
	}
	s.store.MarkFailed(localID, err.Error())
}

// OfflineRecord retrieves a record by local id.
func (s *Service) OfflineRecord(localID string) (store.OfflineRecord, bool) {
	return s.store.OfflineRecord(localID)
}
