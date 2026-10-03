package server

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-feature-flag/studio/internal/auth"
	"github.com/go-feature-flag/studio/internal/config"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/permissions"
	"github.com/go-feature-flag/studio/internal/storage"
)

type Service struct {
	cfg     *config.Config
	repo    storage.Backend
	adapter *goff.Adapter
	perms   *permissions.Set
	notify  *notifier

	cacheMu sync.Mutex
	cache   map[string]cachedFile
	history map[cacheKey]map[string]goff.Flag

	envMu       sync.Mutex
	envNames    []string
	envLoadedAt time.Time
	envTTL      time.Duration
	now         func() time.Time
}

const environmentCacheTTL = 30 * time.Second

type cacheKey struct {
	file string
	sha  string
}

type cachedFile struct {
	sha   string
	flags map[string]goff.Flag
}

func (s *Service) remember(file, sha string, flags []goff.Flag) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	if s.cache == nil {
		s.cache = map[string]cachedFile{}
	}
	if s.history == nil {
		s.history = map[cacheKey]map[string]goff.Flag{}
	}

	byKey := make(map[string]goff.Flag, len(flags))
	for _, f := range flags {
		byKey[f.Key] = f
	}
	s.cache[file] = cachedFile{sha: sha, flags: byKey}
	s.history[cacheKey{file: file, sha: sha}] = byKey

	if len(s.history) > 256 {
		for k := range s.history {
			if k.sha != sha {
				delete(s.history, k)
			}
		}
	}
}

func (s *Service) snapshot(file, sha, key string) (goff.Flag, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	entry, ok := s.history[cacheKey{file: file, sha: sha}]
	if !ok {
		return goff.Flag{}, false
	}
	f, ok := entry[key]
	return f, ok
}

func (s *Service) knownSHA(file, sha string) bool {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	_, ok := s.history[cacheKey{file: file, sha: sha}]
	return ok
}

func NewService(cfg *config.Config, repo storage.Backend, perms *permissions.Set) *Service {
	notify := newNotifier(cfg)
	if notify != nil {
		repo = notifyingBackend{Backend: repo, notify: notify}
	}
	return &Service{cfg: cfg, repo: repo, adapter: goff.New(), perms: perms, notify: notify, envTTL: environmentCacheTTL, now: time.Now}
}

type FlagView struct {
	goff.Flag
	Actions []permissions.Action `json:"actions"`
	Summary string               `json:"summary"`
	FileSHA string               `json:"fileSha"`
}

// A team is one file. Name is the file's basename, never read back from metadata.
type TeamOption struct {
	Name string `json:"name"`
	File string `json:"file"`
}

func teamNameOf(file string) string {
	base := file
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	for _, suffix := range []string{".goff.yaml", ".goff.yml", ".yaml", ".yml"} {
		if trimmed := strings.TrimSuffix(base, suffix); trimmed != base {
			return trimmed
		}
	}
	return base
}

func teamFile(environment, team string) string {
	return environment + "/" + team + ".goff.yaml"
}

// In the single-file layout a team has no file, so permissions match the path it would have had.
func (s *Service) scope(environment, file, team string) string {
	if s.cfg.SingleFile() {
		return teamFile(environment, team)
	}
	return file
}

func (s *Service) scopeOf(v *FlagView) string {
	return s.scope(v.Environment, v.File, v.Team)
}

func (s *Service) teamOf(v *FlagView) string {
	if s.cfg.SingleFile() {
		return v.Team
	}
	return teamNameOf(v.File)
}

func (s *Service) environmentFile(environment string) string {
	return environment + "/" + defaultSeedFile
}

type ListResult struct {
	Flags  []FlagView    `json:"flags"`
	Broken []goff.Broken `json:"broken"`
	Teams  []TeamOption  `json:"teams"`
	// CanCreate stays true with no teams listed, since a new team can be made from the create page.
	CanCreate bool `json:"canCreate"`
}

func (s *Service) List(ctx context.Context, sess auth.Session, environment string) (*ListResult, error) {
	if err := validEnvironment(environment); err != nil {
		return nil, err
	}
	if !s.perms.AllowedAnywhere(sess.Groups, environment, permissions.View) {
		return nil, ErrForbidden
	}

	files, err := s.repo.ListFiles(ctx, environment)
	if err != nil {
		return nil, err
	}

	out := &ListResult{CanCreate: s.perms.AllowedAnywhere(sess.Groups, environment, permissions.Create)}
	seen := map[string]string{}
	teams := map[string]bool{}
	single := s.cfg.SingleFile()

	for _, file := range files {
		if !single && !s.perms.Allowed(permissions.Request{
			Groups: sess.Groups, Environment: environment, File: file, Action: permissions.View,
		}) {
			continue
		}

		f, err := s.repo.ReadFile(ctx, file)
		if err != nil {
			return nil, err
		}

		flags, broken, err := s.adapter.Parse(file, f.Content)
		if err != nil {
			return nil, err
		}
		s.remember(file, f.Version, flags)
		if !single || s.perms.Allowed(permissions.Request{
			Groups: sess.Groups, Environment: environment, File: s.scope(environment, file, ""), Action: permissions.View,
		}) {
			out.Broken = append(out.Broken, broken...)
		}

		for _, flag := range flags {
			scope := s.scope(environment, file, flag.Team)
			if flag.Team != "" {
				teams[flag.Team] = true
			}
			if single && !s.perms.Allowed(permissions.Request{
				Groups: sess.Groups, Environment: environment, File: scope, Action: permissions.View,
			}) {
				continue
			}
			if first, dupe := seen[flag.Key]; dupe {
				out.Broken = append(out.Broken, goff.Broken{
					Key:    flag.Key,
					File:   file,
					Reason: fmt.Sprintf("also defined in %s; flag keys must be unique within an environment", first),
				})
				continue
			}
			seen[flag.Key] = file

			flag.Environment = environment
			out.Flags = append(out.Flags, FlagView{
				Flag:    flag,
				Actions: s.perms.ActionsFor(sess.Groups, environment, scope),
				Summary: Summarize(flag),
				FileSHA: f.Version,
			})
		}
	}

	if single {
		for team := range teams {
			if s.perms.Allowed(permissions.Request{
				Groups: sess.Groups, Environment: environment, File: teamFile(environment, team), Action: permissions.Create,
			}) {
				out.Teams = append(out.Teams, TeamOption{Name: team, File: s.environmentFile(environment)})
			}
		}
	} else {
		for _, file := range files {
			if !s.perms.Allowed(permissions.Request{
				Groups: sess.Groups, Environment: environment, File: file, Action: permissions.Create,
			}) {
				continue
			}
			out.Teams = append(out.Teams, TeamOption{Name: teamNameOf(file), File: file})
		}
	}
	sort.Slice(out.Teams, func(i, j int) bool { return out.Teams[i].Name < out.Teams[j].Name })

	if out.Teams == nil {
		out.Teams = []TeamOption{}
	}
	if out.Flags == nil {
		out.Flags = []FlagView{}
	}
	if out.Broken == nil {
		out.Broken = []goff.Broken{}
	}

	sort.Slice(out.Flags, func(i, j int) bool { return out.Flags[i].Key < out.Flags[j].Key })
	return out, nil
}

func (s *Service) Get(ctx context.Context, sess auth.Session, environment, key string) (*FlagView, error) {
	list, err := s.List(ctx, sess, environment)
	if err != nil {
		return nil, err
	}
	for i := range list.Flags {
		if list.Flags[i].Key == key {
			return &list.Flags[i], nil
		}
	}
	return nil, ErrNotFound
}

type SaveRequest struct {
	Environment string
	Key         string
	File        string
	FileSHA     string
	LoadedFlag  *goff.Flag
	Action      permissions.Action
	Mutate      func(*goff.Flag)
	Summary     string
	Note        ChangeNote
}

type SaveResult struct {
	Commit    string `json:"commit"`
	Retried   bool   `json:"retried"`
	Message   string `json:"message"`
	Unchanged bool   `json:"unchanged,omitempty"`
}

func (s *Service) Snapshot(file, sha, key string) (goff.Flag, bool) {
	return s.snapshot(file, sha, key)
}

func (s *Service) Save(ctx context.Context, sess auth.Session, req SaveRequest) (*SaveResult, error) {
	single := s.cfg.SingleFile()
	if single && !s.perms.AllowedAnywhere(sess.Groups, req.Environment, req.Action) {
		return nil, ErrForbidden
	}
	if !single && !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: req.Environment, File: req.File, Action: req.Action,
	}) {
		return nil, ErrForbidden
	}

	req.Note = req.Note.trimmed()
	if err := req.Note.validate(); err != nil {
		return nil, err
	}

	loadedFlag := req.LoadedFlag
	if loadedFlag == nil && req.FileSHA != "" && !s.knownSHA(req.File, req.FileSHA) {
		return nil, ErrStaleView
	}

	apply := func(current []byte) ([]byte, error) {
		flags, _, err := s.adapter.Parse(req.File, current)
		if err != nil {
			return nil, err
		}

		var target *goff.Flag
		for i := range flags {
			if flags[i].Key == req.Key {
				target = &flags[i]
				break
			}
		}
		if target == nil {
			return nil, ErrNotFound
		}

		if single {
			if err := s.mutateTeamChecked(sess, req, target); err != nil {
				return nil, err
			}
		} else {
			req.Mutate(target)
		}
		stamp(target, s.now(), false)

		next, err := s.adapter.Serialize(current, req.Key, *target)
		if err != nil {
			return nil, err
		}
		if err := s.adapter.Validate(next); err != nil {
			return nil, invalid("refusing to commit an invalid file: %v", err)
		}
		return next, nil
	}

	result, err := s.repo.Write(withNote(ctx, req.Note), storage.ChangeOp{
		Path:        req.File,
		Key:         req.Key,
		BaseVersion: req.FileSHA,
		Message:     req.Note.appendTo(s.commitMessage(req)),
		Apply:       apply,
		Changed:     s.flagChanged(req.File, req.Key, loadedFlag),
	}, identityOf(sess))
	if err != nil {
		return nil, err
	}

	return s.saved(result), nil
}

// Checks the team read from the file being written, so a stale or forged view cannot pick the team.
func (s *Service) mutateTeamChecked(sess auth.Session, req SaveRequest, target *goff.Flag) error {
	allowed := func(team string, action permissions.Action) bool {
		return s.perms.Allowed(permissions.Request{
			Groups: sess.Groups, Environment: req.Environment, File: teamFile(req.Environment, team), Action: action,
		})
	}

	before := goff.TeamOf(target.Metadata)
	if !allowed(before, req.Action) {
		return ErrForbidden
	}
	req.Mutate(target)
	after := goff.TeamOf(target.Metadata)
	if after != before && (!allowed(before, permissions.Delete) || !allowed(after, permissions.Create)) {
		return ErrForbidden
	}
	return nil
}

func sameFlagState(a, b goff.Flag) bool {
	if a.Enabled != b.Enabled || a.Metadata[metaDescription] != b.Metadata[metaDescription] {
		return false
	}
	if !sameOutcome(a.Default, b.Default) {
		return false
	}
	if len(a.Variations) != len(b.Variations) {
		return false
	}
	for i := range a.Variations {
		if a.Variations[i].Name != b.Variations[i].Name {
			return false
		}
		if fmt.Sprintf("%v", a.Variations[i].Value) != fmt.Sprintf("%v", b.Variations[i].Value) {
			return false
		}
	}
	if len(a.Rules) != len(b.Rules) {
		return false
	}
	for i := range a.Rules {
		if a.Rules[i].Name != b.Rules[i].Name ||
			a.Rules[i].Query != b.Rules[i].Query ||
			a.Rules[i].Disabled != b.Rules[i].Disabled ||
			!sameOutcome(a.Rules[i].Outcome, b.Rules[i].Outcome) {
			return false
		}
	}
	return true
}

func sameOutcome(a, b goff.Outcome) bool {
	if a.Variation != b.Variation {
		return false
	}
	if len(a.Percentage) != len(b.Percentage) {
		return false
	}
	for k, v := range a.Percentage {
		if b.Percentage[k] != v {
			return false
		}
	}
	return true
}

func (s *Service) commitMessage(req SaveRequest) string {
	area := strings.TrimSuffix(path.Base(req.File), ".goff.yaml")
	area = strings.TrimSuffix(area, ".yaml")
	area = strings.TrimSuffix(area, ".yml")
	return fmt.Sprintf("[%s] %s/%s: %s", req.Environment, area, req.Key, req.Summary)
}

type invalidError struct{ msg string }

func (e invalidError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return invalidError{msg: fmt.Sprintf(format, args...)}
}

type duplicateKeyError struct{ msg string }

func (e duplicateKeyError) Error() string { return e.msg }

// Team is the only destination the client sends; File is derived from it.
type CreateRequest struct {
	Environment string
	Key         string
	Team        string
	FileSHA     string
	Type        goff.ValueType
	Variations  []goff.Variation
	Default     string
	Enabled     bool
	Description string
}

func (r CreateRequest) scope() string {
	return teamFile(r.Environment, strings.TrimSpace(r.Team))
}

func (s *Service) createFile(r CreateRequest) string {
	if s.cfg.SingleFile() {
		return s.environmentFile(r.Environment)
	}
	return r.scope()
}

type VariationsRequest struct {
	Environment string
	Key         string
	File        string
	FileSHA     string
	Type        goff.ValueType
	Variations  []goff.Variation
	Default     string
	LoadedFlag  *goff.Flag
}

func validKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return invalid("a flag key is required")
	}
	if key != strings.TrimSpace(key) {
		return invalid("a flag key cannot start or end with a space")
	}
	if strings.ContainsAny(key, " \t\n\r\f\v") {
		return invalid("a flag key cannot contain whitespace, try %q", strings.Join(strings.Fields(key), "-"))
	}
	if len(key) > 255 {
		return invalid("a flag key cannot be longer than 255 characters")
	}
	return nil
}

func validTeam(team string) error {
	if strings.TrimSpace(team) == "" {
		return invalid("pick a team; it decides which file the flag lives in")
	}
	return validPathSegment("team name", team)
}

func checkVariationShape(variations []goff.Variation) error {
	if len(variations) == 0 {
		return invalid("a flag needs at least one variation")
	}

	seen := map[string]bool{}
	for _, v := range variations {
		if strings.TrimSpace(v.Name) == "" {
			return invalid("every variation needs a name")
		}
		if err := validName("variation name", v.Name); err != nil {
			return err
		}
		if seen[v.Name] {
			return invalid("variation %q is listed twice", v.Name)
		}
		seen[v.Name] = true
	}
	return nil
}

func checkDefault(variations []goff.Variation, defaultVariation string) error {
	if defaultVariation == "" {
		return invalid("a default variation is required")
	}
	for _, v := range variations {
		if v.Name == defaultVariation {
			return nil
		}
	}
	return invalid("default variation %q is not one of the variations", defaultVariation)
}

func checkVariations(variations []goff.Variation, defaultVariation string) error {
	if err := checkVariationShape(variations); err != nil {
		return err
	}
	return checkDefault(variations, defaultVariation)
}

// Names every reference GOFF would reject, because flag.IsValid only reports the first and never says what points at it.
func brokenReferences(f goff.Flag, variations []goff.Variation, defaultVariation string) []string {
	keep := map[string]bool{}
	for _, v := range variations {
		keep[v.Name] = true
	}

	var problems []string
	note := func(name, where string) {
		if name != "" && !keep[name] {
			problems = append(problems, fmt.Sprintf("variation %q is still used by %s", name, where))
		}
	}

	if defaultVariation == "" {
		note(f.Default.Variation, "the default rule")
	}
	for _, name := range sortedFloatKeys(f.Default.Percentage) {
		note(name, "the default rollout")
	}

	for _, r := range f.Rules {
		where := "a targeting rule"
		if r.Name != "" {
			where = fmt.Sprintf("rule %q", r.Name)
		}
		note(r.Outcome.Variation, where)
		for _, name := range sortedFloatKeys(r.Outcome.Percentage) {
			note(name, where+"'s rollout")
		}
	}
	return problems
}

func sortedFloatKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Service) environmentFiles(ctx context.Context, environment string) ([]string, error) {
	return s.repo.ListFiles(ctx, environment)
}

// Scans every file in the environment, ignoring view permissions, so a key hidden behind them still blocks a create.
func (s *Service) locateKey(ctx context.Context, files []string, key string) (string, bool, error) {
	for _, file := range files {
		f, err := s.repo.ReadFile(ctx, file)
		if err != nil {
			return "", false, err
		}
		flags, broken, err := s.adapter.Parse(file, f.Content)
		if err != nil {
			return "", false, err
		}
		for _, flag := range flags {
			if flag.Key == key {
				return file, true, nil
			}
		}
		for _, b := range broken {
			if b.Key == key {
				return file, true, nil
			}
		}
	}
	return "", false, nil
}

func (s *Service) buildCreate(current []byte, req CreateRequest, file string) ([]byte, error) {
	flags, broken, err := s.adapter.Parse(file, current)
	if err != nil {
		return nil, err
	}
	for _, f := range flags {
		if f.Key == req.Key {
			return nil, duplicateKeyError{msg: fmt.Sprintf("flag %q already exists in %s", req.Key, file)}
		}
	}
	for _, b := range broken {
		if b.Key == req.Key {
			return nil, duplicateKeyError{msg: fmt.Sprintf("flag %q already exists in %s", req.Key, file)}
		}
	}

	created := goff.Flag{
		Key:        req.Key,
		File:       file,
		Type:       req.Type,
		Enabled:    req.Enabled,
		Variations: req.Variations,
		Default:    goff.Outcome{Variation: req.Default},
		Metadata:   map[string]any{"team": strings.TrimSpace(req.Team)},
	}
	if req.Description != "" {
		created.Metadata[metaDescription] = req.Description
	}
	stamp(&created, s.now(), true)

	next, err := s.adapter.Serialize(current, req.Key, created)
	if err != nil {
		return nil, err
	}
	if err := s.adapter.Validate(next); err != nil {
		return nil, invalid("refusing to commit an invalid file: %v", err)
	}
	return next, nil
}

func (s *Service) prepareCreate(ctx context.Context, sess auth.Session, req CreateRequest) error {
	if err := validTeam(req.Team); err != nil {
		return err
	}
	file := s.createFile(req)

	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: req.Environment, File: req.scope(), Action: permissions.Create,
	}) {
		return ErrForbidden
	}
	if err := validKey(req.Key); err != nil {
		return err
	}
	if err := checkVariations(req.Variations, req.Default); err != nil {
		return err
	}
	if err := validDescription(req.Description); err != nil {
		return err
	}

	files, err := s.environmentFiles(ctx, req.Environment)
	if err != nil {
		return err
	}

	found := false
	for _, f := range files {
		if f == file {
			found = true
			break
		}
	}
	if !found && s.cfg.SingleFile() {
		return invalid("%s has no %s yet; recreate the environment or add the file", req.Environment, file)
	}
	if !found {
		return invalid("%q is not a team in %s; create it first", strings.TrimSpace(req.Team), req.Environment)
	}

	if where, taken, err := s.locateKey(ctx, files, req.Key); err != nil {
		return err
	} else if taken {
		return duplicateKeyError{msg: fmt.Sprintf("flag %q already exists in %s; keys must be unique within an environment", req.Key, where)}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, sess auth.Session, req CreateRequest) (*SaveResult, error) {
	if err := s.prepareCreate(ctx, sess, req); err != nil {
		return nil, err
	}
	file := s.createFile(req)
	if req.FileSHA != "" && !s.knownSHA(file, req.FileSHA) {
		return nil, ErrStaleView
	}

	result, err := s.repo.Write(ctx, storage.ChangeOp{
		Path:        file,
		Key:         req.Key,
		BaseVersion: req.FileSHA,
		Message:     s.commitMessage(SaveRequest{Environment: req.Environment, Key: req.Key, File: file, Summary: "created"}),
		Apply:       func(current []byte) ([]byte, error) { return s.buildCreate(current, req, file) },
		// A create only conflicts when the key itself appeared; any other edit to the file just rebases and appends.
		Changed: func(current []byte) (bool, error) { return false, nil },
	}, identityOf(sess))
	if err != nil {
		return nil, err
	}
	return s.saved(result), nil
}

func (s *Service) Delete(ctx context.Context, sess auth.Session, environment, key, fileSHA string, loaded *goff.Flag) (*SaveResult, error) {
	view, err := s.Get(ctx, sess, environment, key)
	if err != nil {
		return nil, err
	}
	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: environment, File: s.scopeOf(view), Action: permissions.Delete,
	}) {
		return nil, ErrForbidden
	}
	if loaded == nil && fileSHA != "" && !s.knownSHA(view.File, fileSHA) {
		return nil, ErrStaleView
	}

	result, err := s.repo.Write(ctx, storage.ChangeOp{
		Path:        view.File,
		Key:         key,
		BaseVersion: fileSHA,
		Message:     s.commitMessage(SaveRequest{Environment: environment, Key: key, File: view.File, Summary: "deleted"}),
		Apply: func(current []byte) ([]byte, error) {
			next, err := s.adapter.Remove(current, key)
			if err != nil {
				return nil, ErrNotFound
			}
			if err := s.adapter.Validate(next); err != nil {
				return nil, invalid("refusing to commit an invalid file: %v", err)
			}
			return next, nil
		},
		Changed: s.flagChanged(view.File, key, loaded),
	}, identityOf(sess))
	if err != nil {
		return nil, err
	}
	return s.saved(result), nil
}

func (s *Service) Rename(ctx context.Context, sess auth.Session, environment, key, newKey, fileSHA string) (*SaveResult, error) {
	view, err := s.Get(ctx, sess, environment, key)
	if err != nil {
		return nil, err
	}

	newKey = strings.TrimSpace(newKey)
	if err := validKey(newKey); err != nil {
		return nil, err
	}
	if newKey == key {
		return nil, invalid("that is already the flag's key")
	}

	// Renaming moves nothing between files, so create rights on the current file are enough.
	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: environment, File: s.scopeOf(view), Action: permissions.Create,
	}) {
		return nil, ErrForbidden
	}
	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: environment, File: s.scopeOf(view), Action: permissions.Delete,
	}) {
		return nil, ErrForbidden
	}
	if fileSHA != "" && !s.knownSHA(view.File, fileSHA) {
		return nil, ErrStaleView
	}

	files, err := s.environmentFiles(ctx, environment)
	if err != nil {
		return nil, err
	}
	if where, taken, err := s.locateKey(ctx, files, newKey); err != nil {
		return nil, err
	} else if taken {
		return nil, duplicateKeyError{msg: fmt.Sprintf("flag %q already exists in %s; keys must be unique within an environment", newKey, where)}
	}

	result, err := s.repo.Write(ctx, storage.ChangeOp{
		Path:        view.File,
		Key:         key,
		BaseVersion: fileSHA,
		Message: s.commitMessage(SaveRequest{
			Environment: environment, Key: key, File: view.File,
			Summary: fmt.Sprintf("renamed to %s", newKey),
		}),
		Apply: func(current []byte) ([]byte, error) {
			next, err := s.adapter.RenameKey(current, key, newKey)
			if err != nil {
				return nil, invalid("%s", err.Error())
			}
			if next, err = s.stampKey(next, view.File, newKey); err != nil {
				return nil, err
			}
			if err := s.adapter.Validate(next); err != nil {
				return nil, invalid("refusing to commit an invalid file: %v", err)
			}
			return next, nil
		},
		Changed: s.flagChanged(view.File, key, nil),
	}, identityOf(sess))
	if err != nil {
		return nil, err
	}
	return s.saved(result), nil
}

func (s *Service) buildVariations(current []byte, req VariationsRequest) ([]byte, error) {
	flags, _, err := s.adapter.Parse(req.File, current)
	if err != nil {
		return nil, err
	}

	var target *goff.Flag
	for i := range flags {
		if flags[i].Key == req.Key {
			target = &flags[i]
			break
		}
	}
	if target == nil {
		return nil, ErrNotFound
	}

	if err := checkVariationShape(req.Variations); err != nil {
		return nil, err
	}
	if problems := brokenReferences(*target, req.Variations, req.Default); len(problems) > 0 {
		return nil, invalid("%s; rename it back or point that rule somewhere else first", strings.Join(problems, "; "))
	}

	defaultVariation := req.Default
	if defaultVariation == "" {
		defaultVariation = target.Default.Variation
	}
	if err := checkDefault(req.Variations, defaultVariation); err != nil {
		return nil, err
	}

	target.Variations = req.Variations
	target.Type = req.Type
	if req.Default != "" {
		target.Default = goff.Outcome{Variation: req.Default}
	}
	stamp(target, s.now(), false)

	next, err := s.adapter.Serialize(current, req.Key, *target)
	if err != nil {
		return nil, err
	}
	if err := s.adapter.Validate(next); err != nil {
		return nil, invalid("refusing to commit an invalid file: %v", err)
	}
	return next, nil
}

func (s *Service) SaveVariations(ctx context.Context, sess auth.Session, req VariationsRequest) (*SaveResult, error) {
	view, err := s.Get(ctx, sess, req.Environment, req.Key)
	if err != nil {
		return nil, err
	}
	req.File = view.File
	if req.Type == "" {
		req.Type = view.Type
	}

	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: req.Environment, File: s.scopeOf(view), Action: permissions.EditVariations,
	}) {
		return nil, ErrForbidden
	}
	if req.LoadedFlag == nil && req.FileSHA != "" && !s.knownSHA(req.File, req.FileSHA) {
		return nil, ErrStaleView
	}

	result, err := s.repo.Write(ctx, storage.ChangeOp{
		Path:        req.File,
		Key:         req.Key,
		BaseVersion: req.FileSHA,
		Message:     s.commitMessage(SaveRequest{Environment: req.Environment, Key: req.Key, File: req.File, Summary: "updated variations"}),
		Apply:       func(current []byte) ([]byte, error) { return s.buildVariations(current, req) },
		Changed:     s.flagChanged(req.File, req.Key, req.LoadedFlag),
	}, identityOf(sess))
	if err != nil {
		return nil, err
	}
	return s.saved(result), nil
}

func (s *Service) flagChanged(file, key string, loaded *goff.Flag) func([]byte) (bool, error) {
	return func(current []byte) (bool, error) {
		if loaded == nil {
			return false, nil
		}
		flags, _, err := s.adapter.Parse(file, current)
		if err != nil {
			return true, nil //nolint:nilerr // an unreadable file counts as changed, so the write backs off
		}
		for _, f := range flags {
			if f.Key == key {
				return !sameFlagState(*loaded, f), nil
			}
		}
		return true, nil
	}
}

func (s *Service) stampKey(content []byte, file, key string) ([]byte, error) {
	flags, _, err := s.adapter.Parse(file, content)
	if err != nil {
		return nil, err
	}
	for i := range flags {
		if flags[i].Key == key {
			stamp(&flags[i], s.now(), false)
			return s.adapter.Serialize(content, key, flags[i])
		}
	}
	return nil, ErrNotFound
}

func (s *Service) saved(result *storage.Result) *SaveResult {
	return &SaveResult{
		Commit:  result.Version,
		Retried: result.Retried,
		Message: fmt.Sprintf("Saved. Live in apps within about %d seconds.", s.cfg.PollSeconds),
	}
}

func identityOf(sess auth.Session) storage.Identity {
	return storage.Identity{Name: sess.DisplayName(), Email: sess.Email, Subject: sess.Subject}
}

func (s *Service) DiffCreate(ctx context.Context, sess auth.Session, req CreateRequest) (*DiffResult, error) {
	if err := s.prepareCreate(ctx, sess, req); err != nil {
		return nil, err
	}

	file, err := s.repo.ReadFile(ctx, s.createFile(req))
	if err != nil {
		return nil, err
	}
	next, err := s.buildCreate(file.Content, req, s.createFile(req))
	if err != nil {
		return nil, err
	}

	return &DiffResult{
		Description: fmt.Sprintf("Create %s in %s: %s", req.Key, s.createFile(req), Summarize(goff.Flag{
			Enabled: req.Enabled, Variations: req.Variations, Default: goff.Outcome{Variation: req.Default},
		})),
		Diff: UnifiedDiff(string(file.Content), string(next), 3),
	}, nil
}

func (s *Service) DiffDelete(ctx context.Context, sess auth.Session, environment, key string) (*DiffResult, error) {
	view, err := s.Get(ctx, sess, environment, key)
	if err != nil {
		return nil, err
	}
	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: environment, File: s.scopeOf(view), Action: permissions.Delete,
	}) {
		return nil, ErrForbidden
	}

	file, err := s.repo.ReadFile(ctx, view.File)
	if err != nil {
		return nil, err
	}
	next, err := s.adapter.Remove(file.Content, key)
	if err != nil {
		return nil, ErrNotFound
	}

	return &DiffResult{
		Description: fmt.Sprintf("Delete %s from %s", key, view.File),
		Diff:        UnifiedDiff(string(file.Content), string(next), 3),
	}, nil
}

func (s *Service) DiffRename(ctx context.Context, sess auth.Session, environment, key, newKey string) (*DiffResult, error) {
	view, err := s.Get(ctx, sess, environment, key)
	if err != nil {
		return nil, err
	}

	newKey = strings.TrimSpace(newKey)
	if err := validKey(newKey); err != nil {
		return nil, err
	}
	for _, action := range []permissions.Action{permissions.Create, permissions.Delete} {
		if !s.perms.Allowed(permissions.Request{
			Groups: sess.Groups, Environment: environment, File: s.scopeOf(view), Action: action,
		}) {
			return nil, ErrForbidden
		}
	}

	files, err := s.environmentFiles(ctx, environment)
	if err != nil {
		return nil, err
	}
	if where, taken, err := s.locateKey(ctx, files, newKey); err != nil {
		return nil, err
	} else if taken {
		return nil, duplicateKeyError{msg: fmt.Sprintf("flag %q already exists in %s; keys must be unique within an environment", newKey, where)}
	}

	file, err := s.repo.ReadFile(ctx, view.File)
	if err != nil {
		return nil, err
	}
	next, err := s.adapter.RenameKey(file.Content, key, newKey)
	if err != nil {
		return nil, invalid("%s", err.Error())
	}

	return &DiffResult{
		Description: fmt.Sprintf("Rename %s to %s in %s", key, newKey, view.File),
		Diff:        UnifiedDiff(string(file.Content), string(next), 3),
	}, nil
}

func (s *Service) DiffVariations(ctx context.Context, sess auth.Session, req VariationsRequest) (*DiffResult, error) {
	view, err := s.Get(ctx, sess, req.Environment, req.Key)
	if err != nil {
		return nil, err
	}
	req.File = view.File
	if req.Type == "" {
		req.Type = view.Type
	}
	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: req.Environment, File: s.scopeOf(view), Action: permissions.EditVariations,
	}) {
		return nil, ErrForbidden
	}

	file, err := s.repo.ReadFile(ctx, req.File)
	if err != nil {
		return nil, err
	}
	next, err := s.buildVariations(file.Content, req)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(req.Variations))
	for _, v := range req.Variations {
		names = append(names, v.Name)
	}

	return &DiffResult{
		Description: fmt.Sprintf("Change the variations on %s to %s", req.Key, strings.Join(names, ", ")),
		Diff:        UnifiedDiff(string(file.Content), string(next), 3),
	}, nil
}

func (s *Service) Attributes(ctx context.Context, sess auth.Session, environment string) ([]string, error) {
	list, err := s.List(ctx, sess, environment)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	for _, f := range list.Flags {
		for _, r := range f.Rules {
			collectAttributes(r.Condition, seen)
		}
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func collectAttributes(c *goff.Condition, seen map[string]bool) {
	if c == nil {
		return
	}
	if c.IsGroup() {
		for _, child := range c.Children {
			collectAttributes(child, seen)
		}
		return
	}
	if c.Attribute != "" {
		seen[c.Attribute] = true
	}
}

type DiffRequest struct {
	Environment string
	Key         string
	Mutate      func(*goff.Flag)
	Description string
	Rollout     *RolloutDiff
}

// RolloutDiff carries the before/after split so the review dialog can show
// them side by side instead of making the reader diff two sentences.
type RolloutDiff struct {
	Before map[string]float64 `json:"before"`
	After  map[string]float64 `json:"after"`
}

type DiffResult struct {
	Description string       `json:"description"`
	Diff        string       `json:"diff"`
	Rollout     *RolloutDiff `json:"rollout,omitempty"`
}

func (s *Service) Diff(ctx context.Context, sess auth.Session, req DiffRequest) (*DiffResult, error) {
	view, err := s.Get(ctx, sess, req.Environment, req.Key)
	if err != nil {
		return nil, err
	}

	file, err := s.repo.ReadFile(ctx, view.File)
	if err != nil {
		return nil, err
	}

	draft := view.Flag
	req.Mutate(&draft)

	next, err := s.adapter.Serialize(file.Content, req.Key, draft)
	if err != nil {
		return nil, err
	}

	description := req.Description
	if description == "" {
		description = fmt.Sprintf("%s becomes: %s", req.Key, Summarize(draft))
	}

	return &DiffResult{
		Description: description,
		Diff:        UnifiedDiff(string(file.Content), string(next), 3),
		Rollout:     req.Rollout,
	}, nil
}

func (s *Service) Preview(ctx context.Context, sess auth.Session, environment, key, targetingKey string, attrs map[string]any, draft *goff.Flag) (goff.EvalResult, error) {
	view, err := s.Get(ctx, sess, environment, key)
	if err != nil {
		return goff.EvalResult{}, err
	}

	file, err := s.repo.ReadFile(ctx, view.File)
	if err != nil {
		return goff.EvalResult{}, err
	}

	content := file.Content
	if draft != nil {
		content, err = s.adapter.Serialize(file.Content, key, *draft)
		if err != nil {
			return goff.EvalResult{}, err
		}
	}

	return s.adapter.Evaluate(content, key, targetingKey, attrs), nil
}

func (s *Service) History(ctx context.Context, sess auth.Session, environment, key string, limit int) ([]storage.Commit, error) {
	if !s.repo.Capabilities().History {
		return nil, nil
	}

	view, err := s.Get(ctx, sess, environment, key)
	if err != nil {
		return nil, err
	}

	want := boundedLimit(limit)
	for scan := want; ; scan = MaxHistoryScan {
		commits, err := s.repo.History(ctx, view.File, scan)
		if err != nil {
			return nil, err
		}
		var out []storage.Commit
		for _, c := range commits {
			if commitTouches(c.Message, key) {
				out = append(out, c)
			}
		}
		if len(out) >= want || len(commits) < scan || scan >= MaxHistoryScan {
			return out[:min(len(out), want)], nil
		}
	}
}

// Studio subjects read "[env] team/key: summary"; anything else matches only on a whole-token key.
func commitTouches(message, key string) bool {
	subject, _, _ := strings.Cut(message, "\n")
	if rest, ok := strings.CutPrefix(subject, "["); ok {
		if _, rest, ok = strings.Cut(rest, "] "); ok {
			if _, rest, ok = strings.Cut(rest, "/"); ok {
				if got, summary, ok := strings.Cut(rest, ": "); ok {
					return got == key || summary == "renamed to "+key
				}
			}
		}
	}
	for i := 0; ; {
		j := strings.Index(subject[i:], key)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(key)
		if (start == 0 || !isKeyByte(subject[start-1])) && (end == len(subject) || !isKeyByte(subject[end])) {
			return true
		}
		i = start + 1
	}
}

func isKeyByte(b byte) bool {
	return b == '-' || b == '_' || b == '.' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func (s *Service) Capabilities() storage.Capabilities {
	return s.repo.Capabilities()
}

type Environment struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
}

func (s *Service) Environments(ctx context.Context, sess auth.Session) ([]Environment, error) {
	names, err := s.environmentNames(ctx)
	if err != nil {
		return nil, err
	}
	out := []Environment{}
	for _, name := range names {
		if s.perms.AllowedAnywhere(sess.Groups, name, permissions.View) {
			out = append(out, Environment{Name: name, Protected: s.cfg.IsProtected(name)})
		}
	}
	return out, nil
}

func (s *Service) CanCreateEnvironments(sess auth.Session) bool {
	return s.perms.CanCreateEnvironments(sess.Groups)
}

func (s *Service) environmentNames(ctx context.Context) ([]string, error) {
	s.envMu.Lock()
	defer s.envMu.Unlock()

	if s.envNames != nil && s.now().Sub(s.envLoadedAt) < s.envTTL {
		return s.envNames, nil
	}
	names, err := s.discoverEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	s.envNames, s.envLoadedAt = names, s.now()
	return names, nil
}

func (s *Service) invalidateEnvironments() {
	s.envMu.Lock()
	defer s.envMu.Unlock()
	s.envNames = nil
}

func (s *Service) discoverEnvironments(ctx context.Context) ([]string, error) {
	dirs, err := s.repo.ListDirectories(ctx, "")
	if errors.Is(err, storage.ErrNotFound) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing environments: %w", err)
	}

	names := []string{}
	for _, dir := range dirs {
		if strings.HasPrefix(dir, ".") || validEnvironment(dir) != nil {
			continue
		}
		files, err := s.repo.ListFiles(ctx, dir)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("listing environment %s: %w", dir, err)
		}
		if len(files) > 0 {
			names = append(names, dir)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *Service) CreateEnvironment(ctx context.Context, sess auth.Session, name, seedFile string) error {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if err := validEnvironment(name); err != nil {
		return err
	}

	if !s.perms.AllowedAnywhere(sess.Groups, name, permissions.Create) {
		return ErrForbidden
	}

	existing, err := s.environmentNames(ctx)
	if err != nil {
		return err
	}
	for _, other := range existing {
		if other == name {
			return fmt.Errorf("%w: environment %q already exists", ErrInvalid, name)
		}
	}

	if s.cfg.SingleFile() {
		seedFile = ""
	}
	seedFile, err = seedFileName(seedFile)
	if err != nil {
		return err
	}

	path := name + "/" + seedFile
	seed := fmt.Sprintf("# Feature flags for %s.\n# Managed by GO Feature Flag Studio.\n", name)

	defer s.invalidateEnvironments()
	return s.repo.CreateFile(ctx, path, []byte(seed), fmt.Sprintf("[%s] created environment", name), identityOf(sess))
}

func (s *Service) CreateTeam(ctx context.Context, sess auth.Session, environment, name string) error {
	if err := validEnvironment(environment); err != nil {
		return err
	}
	name = strings.Trim(strings.TrimSpace(name), "/")
	if err := validTeam(name); err != nil {
		return err
	}

	path := teamFile(environment, name)
	if !s.perms.Allowed(permissions.Request{
		Groups: sess.Groups, Environment: environment, File: path, Action: permissions.Create,
	}) {
		return ErrForbidden
	}
	// A team in the single-file layout is just a label, so it exists once a flag carries it.
	if s.cfg.SingleFile() {
		return nil
	}

	files, err := s.environmentFiles(ctx, environment)
	if err != nil {
		return err
	}
	for _, existing := range files {
		if teamNameOf(existing) == name {
			return invalid("team %q already exists in %s", name, environment)
		}
	}

	seed := fmt.Sprintf("# Feature flags owned by %s in %s.\n# Managed by GO Feature Flag Studio.\n", name, environment)
	defer s.invalidateEnvironments()
	return s.repo.CreateFile(ctx, path, []byte(seed), fmt.Sprintf("[%s] created team %s", environment, name), identityOf(sess))
}

func (s *Service) CachedFilesFor(environment, key string) []string {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	var out []string
	for file, entry := range s.cache {
		if !strings.HasPrefix(file, environment+"/") {
			continue
		}
		if _, ok := entry.flags[key]; ok {
			out = append(out, file)
		}
	}
	sort.Strings(out)
	return out
}
