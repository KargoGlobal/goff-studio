package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/config"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
)

type Server struct {
	cfg    *config.Config
	svc    *Service
	oidc   *auth.OIDC
	sealer *auth.Sealer
	assets fs.FS
}

func New(cfg *config.Config, svc *Service, oidcClient *auth.OIDC, sealer *auth.Sealer, assets fs.FS) *Server {
	return &Server{cfg: cfg, svc: svc, oidc: oidcClient, sealer: sealer, assets: assets}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /auth/login", s.handleLogin)
	mux.HandleFunc("GET /auth/callback", s.handleCallback)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)

	mux.HandleFunc("GET /api/me", s.withSession(s.handleMe))
	mux.HandleFunc("GET /api/environments/{env}/flags", s.withSession(s.handleList))
	mux.HandleFunc("POST /api/environments/{env}/flags", s.withSession(s.handleCreate))
	mux.HandleFunc("GET /api/environments/{env}/flags/{key}", s.withSession(s.handleGet))
	mux.HandleFunc("DELETE /api/environments/{env}/flags/{key}", s.withSession(s.handleDelete))
	mux.HandleFunc("POST /api/environments/{env}/flags/{key}/key", s.withSession(s.handleRename))
	mux.HandleFunc("PUT /api/environments/{env}/flags/{key}/variations", s.withSession(s.handleVariations))
	mux.HandleFunc("POST /api/environments/{env}/flags/{key}/state", s.withSession(s.handleToggle))
	mux.HandleFunc("POST /api/environments/{env}/flags/{key}/rollout", s.withSession(s.handleRollout))
	mux.HandleFunc("POST /api/environments/{env}/flags/{key}/progressive", s.withSession(s.handleProgressive))
	mux.HandleFunc("POST /api/environments/{env}/flags/{key}/experimentation", s.withSession(s.handleExperimentation))
	mux.HandleFunc("POST /api/environments/{env}/flags/{key}/preview", s.withSession(s.handlePreview))
	mux.HandleFunc("GET /api/environments/{env}/flags/{key}/history", s.withSession(s.handleHistory))
	mux.HandleFunc("POST /api/environments/{env}/flags/{key}/diff", s.withSession(s.handleDiff))
	mux.HandleFunc("POST /api/environments/{env}/flags/{key}/rule", s.withSession(s.handleSaveRule))
	mux.HandleFunc("POST /api/environments/{env}/flags/{key}/rules", s.withSession(s.handleAddRule))
	mux.HandleFunc("DELETE /api/environments/{env}/flags/{key}/rules/{rule}", s.withSession(s.handleDeleteRule))
	mux.HandleFunc("PUT /api/environments/{env}/flags/{key}/rules/order", s.withSession(s.handleReorderRules))
	mux.HandleFunc("GET /api/environments/{env}/attributes", s.withSession(s.handleAttributes))
	mux.HandleFunc("POST /api/environments", s.withSession(s.handleCreateEnvironment))
	mux.HandleFunc("POST /api/environments/{env}/teams", s.withSession(s.handleCreateTeam))

	if s.assets != nil {
		mux.Handle("/", s.spa())
	}

	return mux
}

func (s *Server) spa() http.Handler {
	files := http.FileServer(http.FS(s.assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(s.assets, strings.TrimPrefix(r.URL.Path, "/")); err == nil && r.URL.Path != "/" {
			files.ServeHTTP(w, r)
			return
		}
		raw, err := fs.ReadFile(s.assets, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(raw)
	})
}

type handlerWithSession func(http.ResponseWriter, *http.Request, auth.Session)

func (s *Server) withSession(next handlerWithSession) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.sealer.Read(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "please sign in")
			return
		}
		next(w, r, sess)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	challenge, err := s.oidc.Start()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if _, err := s.sealer.WriteStateValue(w, challenge.State); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sealer.WriteVerifier(w, challenge.Verifier)

	http.Redirect(w, r, challenge.URL, http.StatusSeeOther)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if err := s.sealer.CheckState(r, r.URL.Query().Get("state")); err != nil {
		writeError(w, http.StatusBadRequest, "sign-in could not be verified, please try again")
		return
	}
	verifier, err := s.sealer.Verifier(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "sign-in could not be verified, please try again")
		return
	}
	s.sealer.ClearState(w)
	s.sealer.ClearVerifier(w)

	sess, err := s.oidc.Exchange(r.Context(), r.URL.Query().Get("code"), verifier)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.sealer.Write(w, sess); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sealer.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":         sess.DisplayName(),
		"email":        sess.Email,
		"groups":       sess.Groups,
		"environments": s.svc.Environments(r.Context(), sess),
		"pollSeconds":  s.cfg.PollSeconds,
		"capabilities": s.svc.Capabilities(),
	})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	result, err := s.svc.List(r.Context(), sess, r.PathValue("env"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	view, err := s.svc.Get(r.Context(), sess, r.PathValue("env"), r.PathValue("key"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type toggleBody struct {
	Enabled bool   `json:"enabled"`
	FileSHA string `json:"fileSha"`
}

func (s *Server) handleToggle(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body toggleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	env, key := r.PathValue("env"), r.PathValue("key")

	var loaded *goff.Flag
	if body.FileSHA != "" {
		for _, file := range s.candidateFiles(env, key) {
			if snap, ok := s.svc.Snapshot(file, body.FileSHA, key); ok {
				loaded = &snap
				break
			}
		}
	}

	view, err := s.svc.Get(r.Context(), sess, env, key)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	verb := "enabled"
	if !body.Enabled {
		verb = "disabled"
	}

	result, err := s.svc.Save(r.Context(), sess, SaveRequest{
		Environment: env,
		Key:         key,
		File:        view.File,
		FileSHA:     body.FileSHA,
		Action:      permissions.Toggle,
		Summary:     verb,
		LoadedFlag:  loaded,
		Mutate:      func(f *goff.Flag) { f.Enabled = body.Enabled },
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type rolloutBody struct {
	RuleName   string             `json:"ruleName"`
	Percentage map[string]float64 `json:"percentage"`
	FileSHA    string             `json:"fileSha"`
}

func (s *Server) handleRollout(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body rolloutBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}
	if len(body.Percentage) == 0 {
		writeError(w, http.StatusBadRequest, "no percentages given")
		return
	}

	env, key := r.PathValue("env"), r.PathValue("key")

	var loaded *goff.Flag
	if body.FileSHA != "" {
		for _, file := range s.candidateFiles(env, key) {
			if snap, ok := s.svc.Snapshot(file, body.FileSHA, key); ok {
				loaded = &snap
				break
			}
		}
	}

	view, err := s.svc.Get(r.Context(), sess, env, key)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	summary := describeOutcome(goff.Outcome{Percentage: body.Percentage})
	if body.RuleName == "" {
		summary = "default rollout " + summary
	} else {
		summary = body.RuleName + " rollout " + summary
	}

	result, err := s.svc.Save(r.Context(), sess, SaveRequest{
		Environment: env,
		Key:         key,
		File:        view.File,
		FileSHA:     body.FileSHA,
		Action:      permissions.Rollout,
		Summary:     summary,
		LoadedFlag:  loaded,
		Mutate: func(f *goff.Flag) {
			if body.RuleName == "" {
				f.Default = goff.Outcome{Percentage: body.Percentage}
				return
			}
			for i := range f.Rules {
				if f.Rules[i].Name == body.RuleName {
					f.Rules[i].Outcome = goff.Outcome{Percentage: body.Percentage}
				}
			}
		},
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type previewBody struct {
	TargetingKey string         `json:"targetingKey"`
	Attributes   map[string]any `json:"attributes"`
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body previewBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}
	if body.TargetingKey == "" {
		body.TargetingKey = "preview-user"
	}

	result, err := s.svc.Preview(r.Context(), sess, r.PathValue("env"), r.PathValue("key"), body.TargetingKey, body.Attributes, nil)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type diffBody struct {
	Change      string             `json:"change"`
	Enabled     bool               `json:"enabled"`
	RuleName    string             `json:"ruleName"`
	Percentage  map[string]float64 `json:"percentage"`
	Query       string             `json:"query"`
	Outcome     *outcomeBody       `json:"outcome"`
	Team        string             `json:"team"`
	Type        string             `json:"type"`
	Variations  []variationBody    `json:"variations"`
	Default     string             `json:"default"`
	Disabled    *bool              `json:"disabled"`
	Name        string             `json:"name"`
	Variation   string             `json:"variation"`
	Progressive *progressiveSteps  `json:"progressive"`
	Window      *experimentWindow  `json:"experimentation"`
	Order       []string           `json:"order"`
}

type outcomeBody struct {
	Variation  string             `json:"variation"`
	Percentage map[string]float64 `json:"percentage"`
}

func (o *outcomeBody) toOutcome() goff.Outcome {
	if o == nil {
		return goff.Outcome{}
	}
	if len(o.Percentage) > 0 {
		return goff.Outcome{Percentage: o.Percentage}
	}
	return goff.Outcome{Variation: o.Variation}
}

type ruleBody struct {
	RuleName string       `json:"ruleName"`
	Query    string       `json:"query"`
	Outcome  *outcomeBody `json:"outcome"`
	Disabled *bool        `json:"disabled"`
	FileSHA  string       `json:"fileSha"`
}

type variationBody struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type createBody struct {
	Key        string          `json:"key"`
	Team       string          `json:"team"`
	Type       string          `json:"type"`
	Variations []variationBody `json:"variations"`
	Default    string          `json:"default"`
	Enabled    bool            `json:"enabled"`
	FileSHA    string          `json:"fileSha"`
}

type variationsBody struct {
	Type       string          `json:"type"`
	Variations []variationBody `json:"variations"`
	Default    string          `json:"default"`
	FileSHA    string          `json:"fileSha"`
}

func parseType(raw string) (goff.ValueType, error) {
	switch goff.ValueType(strings.TrimSpace(strings.ToLower(raw))) {
	case goff.TypeBool:
		return goff.TypeBool, nil
	case goff.TypeString:
		return goff.TypeString, nil
	case goff.TypeNumber:
		return goff.TypeNumber, nil
	case goff.TypeJSON:
		return goff.TypeJSON, nil
	}
	return "", fmt.Errorf("type must be one of boolean, string, number or json")
}

func coerceVariations(raw []variationBody, t goff.ValueType) ([]goff.Variation, error) {
	out := make([]goff.Variation, 0, len(raw))
	for _, v := range raw {
		value, err := goff.CoerceValue(v.Value, t)
		if err != nil {
			return nil, fmt.Errorf("variation %q: %w", v.Name, err)
		}
		out = append(out, goff.Variation{Name: v.Name, Value: value})
	}
	return out, nil
}

func (s *Server) createRequest(env string, body createBody) (CreateRequest, error) {
	t, err := parseType(body.Type)
	if err != nil {
		return CreateRequest{}, err
	}
	if strings.TrimSpace(body.Team) == "" {
		return CreateRequest{}, fmt.Errorf("a team is required so we know where to put the flag")
	}
	variations, err := coerceVariations(body.Variations, t)
	if err != nil {
		return CreateRequest{}, err
	}
	return CreateRequest{
		Environment: env,
		Key:         strings.TrimSpace(body.Key),
		Team:        strings.TrimSpace(body.Team),
		FileSHA:     body.FileSHA,
		Type:        t,
		Variations:  variations,
		Default:     body.Default,
		Enabled:     body.Enabled,
	}, nil
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body createBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	req, err := s.createRequest(r.PathValue("env"), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.svc.Create(r.Context(), sess, req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	env, key := r.PathValue("env"), r.PathValue("key")
	fileSHA := r.URL.Query().Get("fileSha")

	var loaded *goff.Flag
	if fileSHA != "" {
		for _, file := range s.candidateFiles(env, key) {
			if snap, ok := s.svc.Snapshot(file, fileSHA, key); ok {
				loaded = &snap
				break
			}
		}
	}

	result, err := s.svc.Delete(r.Context(), sess, env, key, fileSHA, loaded)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type renameBody struct {
	Key     string `json:"key"`
	FileSHA string `json:"fileSha"`
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body renameBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	result, err := s.svc.Rename(r.Context(), sess, r.PathValue("env"), r.PathValue("key"), body.Key, body.FileSHA)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) variationsRequest(r *http.Request, sess auth.Session, env, key string, body variationsBody) (VariationsRequest, error) {
	var t goff.ValueType
	if strings.TrimSpace(body.Type) != "" {
		parsed, err := parseType(body.Type)
		if err != nil {
			return VariationsRequest{}, err
		}
		t = parsed
	} else {
		view, err := s.svc.Get(r.Context(), sess, env, key)
		if err != nil {
			return VariationsRequest{}, err
		}
		t = view.Type
	}

	variations, err := coerceVariations(body.Variations, t)
	if err != nil {
		return VariationsRequest{}, err
	}

	var loaded *goff.Flag
	if body.FileSHA != "" {
		for _, file := range s.candidateFiles(env, key) {
			if snap, ok := s.svc.Snapshot(file, body.FileSHA, key); ok {
				loaded = &snap
				break
			}
		}
	}

	return VariationsRequest{
		Environment: env,
		Key:         key,
		FileSHA:     body.FileSHA,
		Type:        t,
		Variations:  variations,
		Default:     body.Default,
		LoadedFlag:  loaded,
	}, nil
}

func (s *Server) handleVariations(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body variationsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	env, key := r.PathValue("env"), r.PathValue("key")

	req, err := s.variationsRequest(r, sess, env, key, body)
	if err != nil {
		writeVariationsRequestError(w, err)
		return
	}

	result, err := s.svc.SaveVariations(r.Context(), sess, req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeVariationsRequestError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		writeServiceError(w, err)
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

type environmentBody struct {
	Name string `json:"name"`
	File string `json:"file"`
}

func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body environmentBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	if err := s.svc.CreateEnvironment(r.Context(), sess, body.Name, body.File); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": body.Name})
}

type teamBody struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body teamBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	if err := s.svc.CreateTeam(r.Context(), sess, r.PathValue("env"), body.Name); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": strings.TrimSpace(body.Name)})
}

func (s *Server) handleAttributes(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	attrs, err := s.svc.Attributes(r.Context(), sess, r.PathValue("env"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if attrs == nil {
		attrs = []string{}
	}
	writeJSON(w, http.StatusOK, attrs)
}

func applyRuleEdit(ruleName, query string, outcome goff.Outcome, disabled *bool) func(*goff.Flag) {
	return func(f *goff.Flag) {
		for i := range f.Rules {
			if f.Rules[i].Name != ruleName {
				continue
			}
			if strings.TrimSpace(query) != "" {
				f.Rules[i].Query = query
				parsed := goff.ParseQuery(query)
				f.Rules[i].Condition = parsed.Condition
				f.Rules[i].Advanced = !parsed.Simple
			}
			if outcome.Variation != "" || len(outcome.Percentage) > 0 {
				f.Rules[i].Outcome = outcome
			}
			if disabled != nil {
				f.Rules[i].Disabled = *disabled
			}
			return
		}
	}
}

type rolloutStepBody struct {
	Variation  string  `json:"variation"`
	Percentage float64 `json:"percentage"`
	Date       string  `json:"date"`
}

type progressiveSteps struct {
	Initial *rolloutStepBody `json:"initial"`
	End     *rolloutStepBody `json:"end"`
}

type progressiveBody struct {
	RuleName  string           `json:"ruleName"`
	Initial   *rolloutStepBody `json:"initial"`
	End       *rolloutStepBody `json:"end"`
	Clear     bool             `json:"clear"`
	Variation string           `json:"variation"`
	FileSHA   string           `json:"fileSha"`
}

func (b rolloutStepBody) toStep() goff.RolloutStep {
	return goff.RolloutStep{Variation: b.Variation, Percentage: b.Percentage, Date: b.Date}
}

// Clearing a rollout must leave the rule serving something, or GOFF rejects the file.
func applyProgressive(ruleName string, next *goff.ProgressiveRollout, fallback string) func(*goff.Flag) {
	return func(f *goff.Flag) {
		for i := range f.Rules {
			if f.Rules[i].Name != ruleName {
				continue
			}
			f.Rules[i].Progressive = next
			if next == nil && fallback != "" {
				f.Rules[i].Outcome = goff.Outcome{Variation: fallback}
			}
			return
		}
	}
}

func (s *Server) handleProgressive(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body progressiveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}
	if strings.TrimSpace(body.RuleName) == "" {
		writeError(w, http.StatusBadRequest, "a rule name is required")
		return
	}

	var next *goff.ProgressiveRollout
	summary := "removed the progressive rollout on " + body.RuleName
	if body.Clear && strings.TrimSpace(body.Variation) == "" {
		writeError(w, http.StatusBadRequest,
			"removing a progressive rollout needs a variation for the rule to serve instead")
		return
	}
	if !body.Clear {
		if body.Initial == nil || body.End == nil {
			writeError(w, http.StatusBadRequest, "a progressive rollout needs both an initial and an end step")
			return
		}
		next = &goff.ProgressiveRollout{Initial: body.Initial.toStep(), End: body.End.toStep()}
		if err := validateProgressive(*next); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		summary = "updated the progressive rollout on " + body.RuleName
	}

	env, key := r.PathValue("env"), r.PathValue("key")

	view, err := s.svc.Get(r.Context(), sess, env, key)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if !ruleExists(view.Rules, body.RuleName) {
		writeError(w, http.StatusNotFound, "there is no rule named "+body.RuleName+" on this flag")
		return
	}
	if err := knownVariations(view.Variations, next); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if fallback := strings.TrimSpace(body.Variation); next == nil && fallback != "" {
		if !hasVariation(view.Variations, fallback) {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("%q is not a variation on this flag", fallback))
			return
		}
	}

	result, err := s.svc.Save(r.Context(), sess, SaveRequest{
		Environment: env,
		Key:         key,
		File:        view.File,
		FileSHA:     body.FileSHA,
		LoadedFlag:  s.snapshotFor(env, key, body.FileSHA),
		Action:      permissions.Rollout,
		Summary:     summary,
		Mutate:      applyProgressive(body.RuleName, next, strings.TrimSpace(body.Variation)),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type experimentWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type experimentationBody struct {
	Start   string `json:"start"`
	End     string `json:"end"`
	Clear   bool   `json:"clear"`
	FileSHA string `json:"fileSha"`
}

func (w *experimentWindow) toExperimentation() *goff.Experimentation {
	if w == nil {
		return nil
	}
	return &goff.Experimentation{Start: strings.TrimSpace(w.Start), End: strings.TrimSpace(w.End)}
}

func applyExperimentation(next *goff.Experimentation) func(*goff.Flag) {
	return func(f *goff.Flag) { f.Experimentation = next }
}

func (s *Server) handleExperimentation(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body experimentationBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	var next *goff.Experimentation
	summary := "removed the experimentation window"
	if !body.Clear {
		next = &goff.Experimentation{
			Start: strings.TrimSpace(body.Start),
			End:   strings.TrimSpace(body.End),
		}
		if err := validateExperimentation(*next); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		summary = "updated the experimentation window"
	}

	env, key := r.PathValue("env"), r.PathValue("key")

	view, err := s.svc.Get(r.Context(), sess, env, key)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.Save(r.Context(), sess, SaveRequest{
		Environment: env,
		Key:         key,
		File:        view.File,
		FileSHA:     body.FileSHA,
		LoadedFlag:  s.snapshotFor(env, key, body.FileSHA),
		Action:      permissions.Rollout,
		Summary:     summary,
		Mutate:      applyExperimentation(next),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GOFF treats both bounds as optional, but a window with neither is meaningless.
func validateExperimentation(e goff.Experimentation) error {
	if e.Start == "" && e.End == "" {
		return fmt.Errorf("an experimentation window needs a start date, an end date, or both")
	}

	var start, end time.Time
	for _, bound := range []struct {
		label string
		raw   string
		into  *time.Time
	}{{"start", e.Start, &start}, {"end", e.End, &end}} {
		if bound.raw == "" {
			continue
		}
		parsed, err := parseDate(bound.label, bound.raw)
		if err != nil {
			return err
		}
		*bound.into = parsed
	}

	if start.IsZero() || end.IsZero() {
		return nil
	}
	return validateOrder(start, end, "start")
}

func ruleExists(rules []goff.Rule, name string) bool {
	for _, r := range rules {
		if r.Name == name {
			return true
		}
	}
	return false
}

func parseDate(label, raw string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("the %s date must look like 2026-01-31T09:00:00Z", label)
	}
	return parsed, nil
}

func validateOrder(start, end time.Time, startLabel string) error {
	if !end.After(start) {
		return fmt.Errorf("the end date must be after the %s date", startLabel)
	}
	return nil
}

func validateProgressive(p goff.ProgressiveRollout) error {
	for label, step := range map[string]goff.RolloutStep{"initial": p.Initial, "end": p.End} {
		if strings.TrimSpace(step.Variation) == "" {
			return fmt.Errorf("the %s step needs a variation", label)
		}
		if step.Percentage < 0 || step.Percentage > 100 {
			return fmt.Errorf("the %s percentage must be between 0 and 100", label)
		}
		if strings.TrimSpace(step.Date) == "" {
			return fmt.Errorf("the %s step needs a date", label)
		}
		if _, err := parseDate(label, step.Date); err != nil {
			return err
		}
	}

	start, _ := time.Parse(time.RFC3339, p.Initial.Date)
	end, _ := time.Parse(time.RFC3339, p.End.Date)
	return validateOrder(start, end, "initial")
}

func hasVariation(variations []goff.Variation, name string) bool {
	for _, v := range variations {
		if v.Name == name {
			return true
		}
	}
	return false
}

func knownVariations(variations []goff.Variation, p *goff.ProgressiveRollout) error {
	if p == nil {
		return nil
	}
	for label, name := range map[string]string{"initial": p.Initial.Variation, "end": p.End.Variation} {
		if !hasVariation(variations, name) {
			return fmt.Errorf("the %s step names %q, which is not a variation on this flag", label, name)
		}
	}
	return nil
}

func (s *Server) handleSaveRule(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body ruleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}
	if body.RuleName == "" {
		writeError(w, http.StatusBadRequest, "a rule name is required")
		return
	}
	if strings.TrimSpace(body.Query) == "" && body.Outcome == nil && body.Disabled == nil {
		writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}

	env, key := r.PathValue("env"), r.PathValue("key")

	var loaded *goff.Flag
	if body.FileSHA != "" {
		for _, file := range s.candidateFiles(env, key) {
			if snap, ok := s.svc.Snapshot(file, body.FileSHA, key); ok {
				loaded = &snap
				break
			}
		}
	}

	view, err := s.svc.Get(r.Context(), sess, env, key)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.Save(r.Context(), sess, SaveRequest{
		Environment: env,
		Key:         key,
		File:        view.File,
		FileSHA:     body.FileSHA,
		LoadedFlag:  loaded,
		Action:      permissions.EditRules,
		Summary:     "updated targeting for " + body.RuleName,
		Mutate:      applyRuleEdit(body.RuleName, body.Query, body.Outcome.toOutcome(), body.Disabled),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body diffBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	env, key := r.PathValue("env"), r.PathValue("key")

	switch body.Change {
	case "create":
		req, err := s.createRequest(env, createBody{
			Key: key, Team: body.Team, Type: body.Type,
			Variations: body.Variations, Default: body.Default, Enabled: body.Enabled,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		result, err := s.svc.DiffCreate(r.Context(), sess, req)
		s.writeDiff(w, result, err)
		return
	case "delete":
		result, err := s.svc.DiffDelete(r.Context(), sess, env, key)
		s.writeDiff(w, result, err)
		return
	case "rename":
		result, err := s.svc.DiffRename(r.Context(), sess, env, key, body.Name)
		s.writeDiff(w, result, err)
		return
	case "variations":
		req, err := s.variationsRequest(r, sess, env, key, variationsBody{
			Type: body.Type, Variations: body.Variations, Default: body.Default,
		})
		if err != nil {
			writeVariationsRequestError(w, err)
			return
		}
		result, err := s.svc.DiffVariations(r.Context(), sess, req)
		s.writeDiff(w, result, err)
		return
	}

	var (
		mutate      func(*goff.Flag)
		description string
	)

	switch body.Change {
	case "state":
		mutate = func(f *goff.Flag) { f.Enabled = body.Enabled }
		if body.Enabled {
			description = "Turn " + key + " on"
		} else {
			description = "Turn " + key + " off for everyone"
		}
	case "rule":
		if strings.TrimSpace(body.Query) == "" && body.Outcome == nil && body.Disabled == nil {
			writeError(w, http.StatusBadRequest, "nothing to change")
			return
		}
		mutate = applyRuleEdit(body.RuleName, body.Query, body.Outcome.toOutcome(), body.Disabled)
		switch {
		case body.Disabled != nil && *body.Disabled:
			description = fmt.Sprintf("Turn off rule %s on %s", body.RuleName, key)
		case body.Disabled != nil:
			description = fmt.Sprintf("Turn on rule %s on %s", body.RuleName, key)
		case body.Outcome != nil && strings.TrimSpace(body.Query) == "":
			description = fmt.Sprintf("Change what %s serves to %s", body.RuleName, describeOutcome(body.Outcome.toOutcome()))
		default:
			description = fmt.Sprintf("Change who %s targets on %s", body.RuleName, key)
		}
	case "addRule":
		if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Query) == "" {
			writeError(w, http.StatusBadRequest, "a new rule needs a name and at least one condition")
			return
		}
		mutate = insertRule(newRule(body.Name, body.Query, body.Outcome.toOutcome()), nil)
		description = fmt.Sprintf("Add rule %s to %s", body.Name, key)
	case "deleteRule":
		if strings.TrimSpace(body.RuleName) == "" {
			writeError(w, http.StatusBadRequest, "which rule?")
			return
		}
		mutate = removeRule(body.RuleName)
		description = fmt.Sprintf("Remove rule %s from %s", body.RuleName, key)
	case "order":
		if len(body.Order) == 0 {
			writeError(w, http.StatusBadRequest, "no order given")
			return
		}
		mutate = reorderRules(body.Order)
		description = fmt.Sprintf("Reorder the rules on %s to: %s", key, strings.Join(body.Order, ", "))
	case "rollout":
		if len(body.Percentage) == 0 {
			writeError(w, http.StatusBadRequest, "no percentages given")
			return
		}
		mutate = func(f *goff.Flag) {
			if body.RuleName == "" {
				f.Default = goff.Outcome{Percentage: body.Percentage}
				return
			}
			for i := range f.Rules {
				if f.Rules[i].Name == body.RuleName {
					f.Rules[i].Outcome = goff.Outcome{Percentage: body.Percentage}
				}
			}
		}
		target := "the default"
		if body.RuleName != "" {
			target = body.RuleName
		}
		description = fmt.Sprintf("Change %s on %s to %s", target, key, describeOutcome(goff.Outcome{Percentage: body.Percentage}))
	case "progressive":
		if body.Progressive == nil || body.Progressive.Initial == nil || body.Progressive.End == nil {
			writeError(w, http.StatusBadRequest, "a progressive rollout needs both an initial and an end step")
			return
		}
		next := &goff.ProgressiveRollout{
			Initial: body.Progressive.Initial.toStep(),
			End:     body.Progressive.End.toStep(),
		}
		if err := validateProgressive(*next); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mutate = applyProgressive(body.RuleName, next, "")
		description = fmt.Sprintf("Update the progressive rollout on %s", body.RuleName)
	case "progressiveClear":
		if strings.TrimSpace(body.Variation) == "" {
			writeError(w, http.StatusBadRequest,
				"removing a progressive rollout needs a variation for the rule to serve instead")
			return
		}
		mutate = applyProgressive(body.RuleName, nil, strings.TrimSpace(body.Variation))
		description = fmt.Sprintf("Remove the progressive rollout on %s, serving %s instead",
			body.RuleName, body.Variation)
	case "experimentation":
		next := body.Window.toExperimentation()
		if next == nil {
			writeError(w, http.StatusBadRequest, "an experimentation window is required")
			return
		}
		if err := validateExperimentation(*next); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mutate = applyExperimentation(next)
		description = fmt.Sprintf("Update the experimentation window on %s", key)
	case "experimentationClear":
		mutate = applyExperimentation(nil)
		description = fmt.Sprintf("Remove the experimentation window on %s", key)
	default:
		writeError(w, http.StatusBadRequest, "unknown change type")
		return
	}

	result, err := s.svc.Diff(r.Context(), sess, DiffRequest{
		Environment: env,
		Key:         key,
		Mutate:      mutate,
		Description: description,
	})
	s.writeDiff(w, result, err)
}

func (s *Server) writeDiff(w http.ResponseWriter, result *DiffResult, err error) {
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	commits, err := s.svc.History(r.Context(), sess, r.PathValue("env"), r.PathValue("key"), limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, commits)
}

func (s *Server) candidateFiles(env, key string) []string {
	return s.svc.CachedFilesFor(env, key)
}

func writeServiceError(w http.ResponseWriter, err error) {
	var (
		bad  invalidError
		dupe duplicateKeyError
	)

	switch {
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.Error())
	case errors.As(err, &dupe):
		writeError(w, http.StatusConflict, dupe.Error())
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "you do not have permission to change this flag")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "that flag no longer exists")
	case errors.Is(err, ErrInvalid):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "invalid request: "))
	case errors.Is(err, ErrStaleView):
		writeError(w, http.StatusConflict, "your view of this flag is out of date, please reload")
	case errors.Is(err, errStorageConflict):
		writeError(w, http.StatusConflict, "someone else just changed this flag, please reload and try again")
	default:
		log.Printf("request failed: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

type addRuleBody struct {
	Name     string       `json:"name"`
	Query    string       `json:"query"`
	Outcome  *outcomeBody `json:"outcome"`
	Position *int         `json:"position"`
	FileSHA  string       `json:"fileSha"`
}

type orderBody struct {
	Order   []string `json:"order"`
	FileSHA string   `json:"fileSha"`
}

func newRule(name, query string, outcome goff.Outcome) goff.Rule {
	parsed := goff.ParseQuery(query)
	return goff.Rule{
		Name:      name,
		Query:     query,
		Condition: parsed.Condition,
		Advanced:  !parsed.Simple,
		Outcome:   outcome,
	}
}

func insertRule(rule goff.Rule, position *int) func(*goff.Flag) {
	return func(f *goff.Flag) {
		at := len(f.Rules)
		if position != nil && *position >= 0 && *position < len(f.Rules) {
			at = *position
		}
		next := make([]goff.Rule, 0, len(f.Rules)+1)
		next = append(next, f.Rules[:at]...)
		next = append(next, rule)
		next = append(next, f.Rules[at:]...)
		f.Rules = next
	}
}

func removeRule(name string) func(*goff.Flag) {
	return func(f *goff.Flag) {
		next := make([]goff.Rule, 0, len(f.Rules))
		for _, r := range f.Rules {
			if r.Name != name {
				next = append(next, r)
			}
		}
		f.Rules = next
	}
}

func reorderRules(order []string) func(*goff.Flag) {
	return func(f *goff.Flag) {
		byName := make(map[string]goff.Rule, len(f.Rules))
		for _, r := range f.Rules {
			byName[r.Name] = r
		}
		next := make([]goff.Rule, 0, len(order))
		for _, name := range order {
			if r, ok := byName[name]; ok {
				next = append(next, r)
			}
		}
		f.Rules = next
	}
}

func validOutcome(f goff.Flag, o goff.Outcome) error {
	known := map[string]bool{}
	for _, v := range f.Variations {
		known[v.Name] = true
	}

	if len(o.Percentage) > 0 {
		for name := range o.Percentage {
			if !known[name] {
				return fmt.Errorf("variation %q does not exist on this flag", name)
			}
		}
		return nil
	}
	if o.Variation == "" {
		return fmt.Errorf("a rule must serve a variation or a percentage split")
	}
	if !known[o.Variation] {
		return fmt.Errorf("variation %q does not exist on this flag", o.Variation)
	}
	return nil
}

func isPermutation(current []goff.Rule, order []string) error {
	if len(order) != len(current) {
		return fmt.Errorf("the new order lists %d rules but this flag has %d; reordering must not add or drop rules", len(order), len(current))
	}

	want := map[string]int{}
	for _, r := range current {
		want[r.Name]++
	}
	for _, name := range order {
		if want[name] == 0 {
			return fmt.Errorf("rule %q is not part of this flag", name)
		}
		want[name]--
	}
	return nil
}

func (s *Server) handleAddRule(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body addRuleBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "a rule needs a name")
		return
	}
	if strings.TrimSpace(body.Query) == "" {
		writeError(w, http.StatusBadRequest, "a rule needs at least one condition")
		return
	}

	env, key := r.PathValue("env"), r.PathValue("key")
	view, err := s.svc.Get(r.Context(), sess, env, key)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	for _, existing := range view.Rules {
		if existing.Name == name {
			writeError(w, http.StatusConflict, "a rule named "+name+" already exists on this flag")
			return
		}
	}

	outcome := body.Outcome.toOutcome()
	if err := validOutcome(view.Flag, outcome); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.svc.Save(r.Context(), sess, SaveRequest{
		Environment: env,
		Key:         key,
		File:        view.File,
		FileSHA:     body.FileSHA,
		LoadedFlag:  s.snapshotFor(env, key, body.FileSHA),
		Action:      permissions.EditRules,
		Summary:     "added rule " + name,
		Mutate:      insertRule(newRule(name, body.Query, outcome), body.Position),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	env, key, name := r.PathValue("env"), r.PathValue("key"), r.PathValue("rule")

	view, err := s.svc.Get(r.Context(), sess, env, key)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	found := false
	for _, existing := range view.Rules {
		if existing.Name == name {
			found = true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "there is no rule named "+name+" on this flag")
		return
	}

	sha := r.URL.Query().Get("fileSha")
	result, err := s.svc.Save(r.Context(), sess, SaveRequest{
		Environment: env,
		Key:         key,
		File:        view.File,
		FileSHA:     sha,
		LoadedFlag:  s.snapshotFor(env, key, sha),
		Action:      permissions.EditRules,
		Summary:     "deleted rule " + name,
		Mutate:      removeRule(name),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleReorderRules(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	var body orderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request")
		return
	}

	env, key := r.PathValue("env"), r.PathValue("key")
	view, err := s.svc.Get(r.Context(), sess, env, key)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	if err := isPermutation(view.Rules, body.Order); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.svc.Save(r.Context(), sess, SaveRequest{
		Environment: env,
		Key:         key,
		File:        view.File,
		FileSHA:     body.FileSHA,
		LoadedFlag:  s.snapshotFor(env, key, body.FileSHA),
		Action:      permissions.EditRules,
		Summary:     "reordered rules",
		Mutate:      reorderRules(body.Order),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) snapshotFor(env, key, sha string) *goff.Flag {
	if sha == "" {
		return nil
	}
	for _, file := range s.candidateFiles(env, key) {
		if snap, ok := s.svc.Snapshot(file, sha, key); ok {
			return &snap
		}
	}
	return nil
}
