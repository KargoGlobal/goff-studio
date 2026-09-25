package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/experiments"
)

type experimentBody struct {
	Experiment *experiments.Experiment `json:"experiment"`
	FileSHA    string                  `json:"fileSha"`
	Create     bool                    `json:"create"`
}

type metricBody struct {
	Metric  *experiments.Metric `json:"metric"`
	FileSHA string              `json:"fileSha"`
	Create  bool                `json:"create"`
}

func (s *Server) experimentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/experiments", s.withSession(s.handleListExperiments))
	mux.HandleFunc("POST /api/experiments", s.withSession(s.handleCreateExperiment))
	mux.HandleFunc("POST /api/experiments/power", s.withSession(s.handlePower))
	mux.HandleFunc("GET /api/experiments/{key}", s.withSession(s.handleGetExperiment))
	mux.HandleFunc("PUT /api/experiments/{key}", s.withSession(s.handleUpdateExperiment))
	mux.HandleFunc("POST /api/experiments/{key}/diff", s.withSession(s.handleDiffExperiment))
	mux.HandleFunc("GET /api/experiments/{key}/results", s.withSession(s.handleResults))

	mux.HandleFunc("GET /api/metrics", s.withSession(s.handleListMetrics))
	mux.HandleFunc("POST /api/metrics", s.withSession(s.handleCreateMetric))
	mux.HandleFunc("GET /api/metrics/{key}", s.withSession(s.handleGetMetric))
	mux.HandleFunc("PUT /api/metrics/{key}", s.withSession(s.handleUpdateMetric))
	mux.HandleFunc("POST /api/metrics/{key}/diff", s.withSession(s.handleDiffMetric))
}

func (s *Server) handleListExperiments(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	list, err := s.svc.ListExperiments(r.Context(), sess)
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetExperiment(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	view, err := s.svc.GetExperiment(r.Context(), sess, r.PathValue("key"))
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func readExperiment(w http.ResponseWriter, r *http.Request) (experimentBody, bool) {
	var body experimentBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the experiment; check the dates are complete")
		return body, false
	}
	if body.Experiment == nil {
		writeError(w, http.StatusBadRequest, "no experiment given")
		return body, false
	}
	return body, true
}

func (s *Server) handleCreateExperiment(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	body, ok := readExperiment(w, r)
	if !ok {
		return
	}
	result, err := s.svc.SaveExperiment(r.Context(), sess, ExperimentChange{Experiment: *body.Experiment, Create: true})
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleUpdateExperiment(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	body, ok := readExperiment(w, r)
	if !ok {
		return
	}
	result, err := s.svc.SaveExperiment(r.Context(), sess, ExperimentChange{
		Key: r.PathValue("key"), Experiment: *body.Experiment, FileSHA: body.FileSHA,
	})
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDiffExperiment(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	body, ok := readExperiment(w, r)
	if !ok {
		return
	}
	result, err := s.svc.DiffExperiment(r.Context(), sess, ExperimentChange{
		Key: r.PathValue("key"), Experiment: *body.Experiment, Create: body.Create,
	})
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	raw, err := s.svc.ExperimentResults(r.Context(), sess, r.PathValue("key"), r.URL.Query().Get("as_of"))
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, max-age=60")
	_, _ = w.Write(raw) //nolint:gosec // JSON from the analysis service or json.Marshal, served as application/json
}

func (s *Server) handlePower(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var req experiments.PowerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}
	raw, err := s.svc.Power(r.Context(), sess, req)
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw) //nolint:gosec // JSON from the analysis service or json.Marshal, served as application/json
}

func (s *Server) handleListMetrics(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	list, err := s.svc.ListMetrics(r.Context(), sess)
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetMetric(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	view, err := s.svc.GetMetric(r.Context(), sess, r.PathValue("key"))
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func readMetric(w http.ResponseWriter, r *http.Request) (metricBody, bool) {
	var body metricBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the metric")
		return body, false
	}
	if body.Metric == nil {
		writeError(w, http.StatusBadRequest, "no metric given")
		return body, false
	}
	return body, true
}

func (s *Server) handleCreateMetric(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	body, ok := readMetric(w, r)
	if !ok {
		return
	}
	result, err := s.svc.SaveMetric(r.Context(), sess, MetricChange{Metric: *body.Metric, Create: true})
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleUpdateMetric(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	body, ok := readMetric(w, r)
	if !ok {
		return
	}
	result, err := s.svc.SaveMetric(r.Context(), sess, MetricChange{
		Key: r.PathValue("key"), Metric: *body.Metric, FileSHA: body.FileSHA,
	})
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDiffMetric(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	body, ok := readMetric(w, r)
	if !ok {
		return
	}
	result, err := s.svc.DiffMetric(r.Context(), sess, MetricChange{
		Key: r.PathValue("key"), Metric: *body.Metric, Create: body.Create,
	})
	if err != nil {
		writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeExperimentError(w http.ResponseWriter, err error) {
	var upstream *experiments.UpstreamError
	switch {
	case errors.Is(err, ErrExperimentNotFound), errors.Is(err, ErrMetricNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "you do not have permission to do that")
	case errors.Is(err, ErrStaleView):
		writeError(w, http.StatusConflict, "your view of this is out of date, please reload")
	case errors.Is(err, errStorageConflict):
		writeError(w, http.StatusConflict, "someone else just changed this, please reload and try again")
	case errors.Is(err, experiments.ErrNoResults):
		writeError(w, http.StatusNotFound, "There are no results for this experiment yet. They appear once the first day of data is processed.")
	case errors.Is(err, experiments.ErrUnavailable):
		log.Printf("analysis service: %v", err)
		writeError(w, http.StatusGatewayTimeout, "The analysis service did not answer in time. Try again in a minute.")
	case errors.As(err, &upstream):
		log.Printf("analysis service: %v", err)
		writeError(w, http.StatusBadGateway, "The analysis service could not produce results right now. Try again later.")
	default:
		writeServiceError(w, err)
	}
}
