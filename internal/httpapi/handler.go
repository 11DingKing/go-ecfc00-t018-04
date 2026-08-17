// Package httpapi exposes the patrol dispatch service over HTTP using only the
// standard library. It translates JSON requests into application service calls
// and renders domain results as JSON. Routing uses Go 1.22+ method-pattern
// ServeMux so path parameters are first-class.
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"qilian-patrol/internal/app"
	"qilian-patrol/internal/domain"
	"qilian-patrol/internal/store"
	"qilian-patrol/internal/sync"
)

// Server wires the application service and background syncer to HTTP handlers.
type Server struct {
	svc    *app.Service
	syncer *sync.Syncer
	mux    *http.ServeMux
}

// New constructs a Server.
func New(svc *app.Service, sy *sync.Syncer) *Server {
	s := &Server{svc: svc, syncer: sy, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the configured http.Handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.healthz)

	s.mux.HandleFunc("POST /api/grids", s.createGrid)
	s.mux.HandleFunc("GET /api/grids", s.listGrids)
	s.mux.HandleFunc("POST /api/checkpoints", s.createCheckpoint)
	s.mux.HandleFunc("POST /api/equipment", s.createEquipment)
	s.mux.HandleFunc("GET /api/equipment", s.listEquipment)

	s.mux.HandleFunc("POST /api/workorders", s.createWorkOrder)
	s.mux.HandleFunc("GET /api/workorders", s.listWorkOrders)
	s.mux.HandleFunc("GET /api/workorders/{id}", s.getWorkOrder)
	s.mux.HandleFunc("POST /api/workorders/{id}/dispatch", s.dispatch)
	s.mux.HandleFunc("POST /api/workorders/{id}/checkin", s.checkIn)
	s.mux.HandleFunc("POST /api/workorders/{id}/start", s.startPatrol)
	s.mux.HandleFunc("POST /api/workorders/{id}/complete", s.completePatrol)
	s.mux.HandleFunc("POST /api/workorders/{id}/verify", s.verifyPatrol)
	s.mux.HandleFunc("POST /api/workorders/{id}/cancel", s.cancel)

	s.mux.HandleFunc("POST /api/equipment/{code}/claim", s.claimEquipment)
	s.mux.HandleFunc("POST /api/equipment/{code}/issue", s.issueEquipment)
	s.mux.HandleFunc("POST /api/equipment/{code}/return", s.returnEquipment)

	s.mux.HandleFunc("POST /api/incidents", s.reportIncident)
	s.mux.HandleFunc("GET /api/incidents", s.listIncidents)
	s.mux.HandleFunc("POST /api/incidents/{id}/assign", s.assignIncident)
	s.mux.HandleFunc("POST /api/incidents/{id}/handle", s.handleIncident)
	s.mux.HandleFunc("POST /api/incidents/{id}/close", s.closeIncident)

	s.mux.HandleFunc("POST /api/tracks", s.submitTrack)
	s.mux.HandleFunc("GET /api/tracks/{workOrderId}", s.getTrack)

	s.mux.HandleFunc("POST /api/sync/offline", s.enqueueOffline)
	s.mux.HandleFunc("GET /api/sync/status", s.syncStatus)
	s.mux.HandleFunc("POST /api/sync/online", s.setOnline)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now().UTC()})
}

// --- Grids / Checkpoints / Equipment setup ---

func (s *Server) createGrid(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		ZoneType domain.ZoneType `json:"zoneType"`
		Route    []domain.Point  `json:"route"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	g := domain.Grid{ID: req.ID, Name: req.Name, ZoneType: req.ZoneType, Route: req.Route}
	if err := s.svc.SaveGrid(g); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) listGrids(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.Grids())
}

func (s *Server) createCheckpoint(w http.ResponseWriter, r *http.Request) {
	var c domain.Checkpoint
	if err := decode(r, &c); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.svc.SaveCheckpoint(c); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) createEquipment(w http.ResponseWriter, r *http.Request) {
	var e domain.Equipment
	if err := decode(r, &e); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if e.Status == "" {
		e.Status = domain.EquipAvailable
	}
	if err := s.svc.SaveEquipment(e); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) listEquipment(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.EquipmentList())
}

// --- Work orders ---

func (s *Server) createWorkOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GridID       string    `json:"gridId"`
		CheckpointID string    `json:"checkpointId"`
		AssigneeIDs  []string  `json:"assigneeIds"`
		Priority     int       `json:"priority"`
		Reported     bool      `json:"reported"`
		PlannedStart time.Time `json:"plannedStart"`
		ShiftEnd     time.Time `json:"shiftEnd"`
		RequestKey   string    `json:"requestKey"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	w2, err := s.svc.CreateWorkOrder(app.CreateWorkOrderRequest{
		GridID:       req.GridID,
		CheckpointID: req.CheckpointID,
		AssigneeIDs:  req.AssigneeIDs,
		Priority:     req.Priority,
		Reported:     req.Reported,
		PlannedStart: req.PlannedStart,
		ShiftEnd:     req.ShiftEnd,
		RequestKey:   req.RequestKey,
	})
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, w2)
}

func (s *Server) listWorkOrders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.WorkOrders())
}

func (s *Server) getWorkOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wo, ok := s.svc.WorkOrder(id)
	if !ok {
		respondError(w, http.StatusNotFound, errors.New("work order not found"))
		return
	}
	writeJSON(w, http.StatusOK, wo)
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	wo, err := s.svc.Dispatch(r.PathValue("id"))
	respondMutation(w, wo, err)
}

func (s *Server) checkIn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OfficerID string `json:"officerId"`
		QRCode    string `json:"qrCode"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	wo, err := s.svc.CheckIn(r.PathValue("id"), req.OfficerID, req.QRCode)
	respondMutation(w, wo, err)
}

func (s *Server) startPatrol(w http.ResponseWriter, r *http.Request) {
	wo, err := s.svc.StartPatrol(r.PathValue("id"))
	respondMutation(w, wo, err)
}

func (s *Server) completePatrol(w http.ResponseWriter, r *http.Request) {
	wo, err := s.svc.CompletePatrol(r.PathValue("id"))
	respondMutation(w, wo, err)
}

func (s *Server) verifyPatrol(w http.ResponseWriter, r *http.Request) {
	wo, err := s.svc.VerifyPatrol(r.PathValue("id"))
	respondMutation(w, wo, err)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	wo, err := s.svc.Cancel(r.PathValue("id"))
	respondMutation(w, wo, err)
}

// --- Equipment ---

func (s *Server) claimEquipment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OfficerID string `json:"officerId"`
		Priority  int    `json:"priority"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	eq, res, err := s.svc.ClaimEquipment(r.PathValue("code"), req.OfficerID, req.Priority)
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"equipment": eq, "claim": res})
}

func (s *Server) issueEquipment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OfficerID string `json:"officerId"`
		OrderID   string `json:"orderId"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	eq, err := s.svc.IssueEquipment(r.PathValue("code"), req.OfficerID, req.OrderID)
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, eq)
}

func (s *Server) returnEquipment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OfficerID string                    `json:"officerId"`
		Condition domain.EquipmentCondition `json:"condition"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	if req.Condition == "" {
		req.Condition = domain.CondGood
	}
	eq, err := s.svc.ReturnEquipment(r.PathValue("code"), req.OfficerID, req.Condition)
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, eq)
}

// --- Incidents ---

func (s *Server) reportIncident(w http.ResponseWriter, r *http.Request) {
	var req app.ReportIncidentRequest
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	in, err := s.svc.ReportIncident(req)
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.Incidents())
}

func (s *Server) assignIncident(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HandlerID string `json:"handlerId"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	in, err := s.svc.AssignIncident(r.PathValue("id"), req.HandlerID)
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) handleIncident(w http.ResponseWriter, r *http.Request) {
	in, err := s.svc.StartHandling(r.PathValue("id"))
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) closeIncident(w http.ResponseWriter, r *http.Request) {
	in, err := s.svc.CloseIncident(r.PathValue("id"))
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// --- Tracks ---

func (s *Server) submitTrack(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkOrderID string              `json:"workOrderId"`
		Points      []domain.TrackPoint `json:"points"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.svc.SubmitTrack(req.WorkOrderID, req.Points, false)
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) getTrack(w http.ResponseWriter, r *http.Request) {
	t, ok := s.svc.Track(r.PathValue("workOrderId"))
	if !ok {
		respondError(w, http.StatusNotFound, errors.New("track not found"))
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// --- Offline sync ---

func (s *Server) enqueueOffline(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Records []store.OfflineRecord `json:"records"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	results := make([]map[string]any, 0, len(req.Records))
	for _, rec := range req.Records {
		stored, created := s.svc.EnqueueOffline(rec)
		results = append(results, map[string]any{
			"localId": stored.LocalID,
			"status":  stored.Status,
			"created": created,
		})
	}
	// Attempt an immediate reconcile tick so connected clients see fast closure.
	s.syncer.TickOnce()
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": results, "stats": s.svc.OfflineStats()})
}

func (s *Server) syncStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"online":      s.syncer.IsOnline(),
		"stats":       s.svc.OfflineStats(),
		"syncedCount": s.syncer.SyncedCount(),
		"failedCount": s.syncer.FailedCount(),
		"slaFlagged":  s.syncer.SLAFlagged(),
	})
}

func (s *Server) setOnline(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Online bool `json:"online"`
	}
	if err := decode(r, &req); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	s.syncer.SetOnline(req.Online)
	if req.Online {
		s.syncer.TickOnce()
	}
	writeJSON(w, http.StatusOK, map[string]any{"online": s.syncer.IsOnline(), "stats": s.svc.OfflineStats()})
}

// --- helpers ---

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response: %v", err)
	}
}

type errBody struct {
	Error string `json:"error"`
}

func respondError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errBody{Error: err.Error()})
}

func respondMutation(w http.ResponseWriter, wo domain.WorkOrder, err error) {
	if err != nil {
		respondError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, wo)
}

// statusFor maps domain/application errors to HTTP status codes.
func statusFor(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrDuplicateOrder):
		return http.StatusConflict
	case errors.Is(err, domain.ErrInvalidTransition),
		errors.Is(err, domain.ErrOrderTerminal),
		errors.Is(err, domain.ErrReturnAfterShift),
		errors.Is(err, domain.ErrCoreZoneAssignees),
		errors.Is(err, domain.ErrCoreZoneReport),
		errors.Is(err, app.ErrEquipmentOutstanding),
		errors.Is(err, app.ErrTrackNotSubmitted),
		errors.Is(err, app.ErrCannotIssue),
		errors.Is(err, app.ErrNotAssignee),
		errors.Is(err, app.ErrCheckpointMismatch),
		errors.Is(err, app.ErrCheckpointGridMismatch),
		errors.Is(err, app.ErrOfficerRequired),
		errors.Is(err, domain.ErrNotHolder),
		errors.Is(err, domain.ErrEquipNotLocked),
		errors.Is(err, domain.ErrEquipNotIssued):
		return http.StatusUnprocessableEntity
	case strings.Contains(msg, "required"):
		return http.StatusBadRequest
	}
	return http.StatusBadRequest
}

// ServeHTTP makes Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
