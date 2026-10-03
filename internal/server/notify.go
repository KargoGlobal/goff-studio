package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-feature-flag/studio/internal/config"
	"github.com/go-feature-flag/studio/internal/goff"
	"github.com/go-feature-flag/studio/internal/storage"
)

const (
	EventFlagChanged = "flag.changed"
	notifyTimeout    = 10 * time.Second
	notifyAttempts   = 3
	signatureHeader  = "X-Studio-Signature"
	eventHeader      = "X-Studio-Event"
	deliveryHeader   = "X-Studio-Delivery"
	maxNoteReason    = 500
	maxNoteReference = 200
	referenceTrailer = "Reference: "
	reasonTrailer    = "Reason: "
)

var notifyRetryBackoff = 500 * time.Millisecond

// ChangeNote says why a change was made; automation sets it so the history and notifications explain an incident action.
type ChangeNote struct {
	Reason    string `json:"reason,omitempty"`
	Reference string `json:"reference,omitempty"`
}

func (n ChangeNote) empty() bool { return n.Reason == "" && n.Reference == "" }

func (n ChangeNote) validate() error {
	if hasControlChars(n.Reason) || hasControlChars(n.Reference) {
		return invalid("a reason or reference cannot contain line breaks or control characters")
	}
	if len([]rune(n.Reason)) > maxNoteReason {
		return invalid("a reason cannot be longer than %d characters", maxNoteReason)
	}
	if len([]rune(n.Reference)) > maxNoteReference {
		return invalid("a reference cannot be longer than %d characters", maxNoteReference)
	}
	return nil
}

func (n ChangeNote) trimmed() ChangeNote {
	return ChangeNote{Reason: strings.TrimSpace(n.Reason), Reference: strings.TrimSpace(n.Reference)}
}

func (n ChangeNote) appendTo(message string) string {
	if n.empty() {
		return message
	}
	var b strings.Builder
	b.WriteString(message)
	b.WriteString("\n")
	if n.Reason != "" {
		b.WriteString("\n" + reasonTrailer + n.Reason)
	}
	if n.Reference != "" {
		b.WriteString("\n" + referenceTrailer + n.Reference)
	}
	return b.String()
}

type noteKey struct{}

func withNote(ctx context.Context, note ChangeNote) context.Context {
	if note.empty() {
		return ctx
	}
	return context.WithValue(ctx, noteKey{}, note)
}

func noteFrom(ctx context.Context) ChangeNote {
	note, _ := ctx.Value(noteKey{}).(ChangeNote)
	return note
}

type Actor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	ID    string `json:"id"`
}

type ChangeEvent struct {
	Event       string    `json:"event"`
	Environment string    `json:"environment"`
	Team        string    `json:"team"`
	Flag        string    `json:"flag"`
	File        string    `json:"file"`
	Summary     string    `json:"summary"`
	Enabled     *bool     `json:"enabled,omitempty"`
	Message     string    `json:"message"`
	Reason      string    `json:"reason,omitempty"`
	Reference   string    `json:"reference,omitempty"`
	Version     string    `json:"version"`
	Actor       Actor     `json:"actor"`
	URL         string    `json:"url,omitempty"`
	At          time.Time `json:"at"`
}

type notifier struct {
	hooks   []config.Notification
	cfg     *config.Config
	adapter *goff.Adapter
	client  *http.Client
	now     func() time.Time
	wg      sync.WaitGroup
}

func newNotifier(cfg *config.Config) *notifier {
	if len(cfg.Notifications) == 0 {
		return nil
	}
	return &notifier{
		hooks:   cfg.Notifications,
		cfg:     cfg,
		adapter: goff.New(),
		client:  &http.Client{Timeout: notifyTimeout},
		now:     time.Now,
	}
}

// notifyingBackend wraps storage so every write path, promotion included, is reported exactly once.
type notifyingBackend struct {
	storage.Backend
	notify *notifier
}

func (b notifyingBackend) Write(ctx context.Context, op storage.ChangeOp, who storage.Identity) (*storage.Result, error) {
	// The last successful Apply is what was written; it holds the flag's team and new state.
	var before, after []byte
	apply := op.Apply
	op.Apply = func(current []byte) ([]byte, error) {
		next, err := apply(current)
		if err == nil {
			before, after = current, next
		}
		return next, err
	}

	result, err := b.Backend.Write(ctx, op, who)
	// Object-store backends report the current version for a write that changed nothing; that is not a change.
	if err == nil && result != nil && result.Version != "" && !bytes.Equal(before, after) {
		b.notify.send(b.notify.event(op, who, result.Version, noteFrom(ctx), before, after))
	}
	return result, err
}

func (n *notifier) find(file, key string, content []byte) (goff.Flag, bool) {
	flags, _, err := n.adapter.Parse(file, content)
	if err != nil {
		return goff.Flag{}, false
	}
	for _, f := range flags {
		if f.Key == key {
			return f, true
		}
	}
	return goff.Flag{}, false
}

func (n *notifier) event(op storage.ChangeOp, who storage.Identity, version string, note ChangeNote, before, after []byte) ChangeEvent {
	environment, _, _ := strings.Cut(op.Path, "/")
	subject, _, _ := strings.Cut(op.Message, "\n")
	summary := subject
	if _, rest, ok := strings.Cut(subject, ": "); ok {
		summary = rest
	}

	ev := ChangeEvent{
		Event:       EventFlagChanged,
		Environment: environment,
		Team:        teamNameOf(op.Path),
		Flag:        op.Key,
		File:        op.Path,
		Summary:     summary,
		Message:     subject,
		Reason:      note.Reason,
		Reference:   note.Reference,
		Version:     version,
		Actor:       Actor{Name: who.Name, Email: who.Email, ID: who.Subject},
		At:          n.now().UTC(),
	}

	flag, found := n.find(op.Path, op.Key, after)
	if found {
		enabled := flag.Enabled
		ev.Enabled = &enabled
	} else {
		flag, found = n.find(op.Path, op.Key, before)
	}
	if n.cfg.SingleFile() {
		ev.Team = ""
		if found {
			ev.Team = goff.TeamOf(flag.Metadata)
		}
	}

	if base := strings.TrimRight(n.cfg.Server.BaseURL, "/"); base != "" && op.Key != "" {
		ev.URL = base + "/env/" + url.PathEscape(environment) + "/flags/" + url.PathEscape(op.Key)
	}
	return ev
}

// send never blocks the save: a slow or failing receiver must not stop someone turning a flag off.
func (n *notifier) send(ev ChangeEvent) {
	for _, hook := range n.hooks {
		if !hook.NotifiesFor(ev.Environment) {
			continue
		}
		body, err := payload(hook.Format, ev)
		if err != nil {
			log.Printf("notification for %q/%q: %v", ev.Environment, ev.Flag, err)
			continue
		}
		n.wg.Add(1)
		go func(hook config.Notification) {
			defer n.wg.Done()
			if err := n.deliver(hook, body, deliveryID()); err != nil {
				log.Printf("notification for %q/%q to %s failed: %v", ev.Environment, ev.Flag, redactURL(hook.URL), err) //nolint:gosec // %q escapes the only request-derived values
			}
		}(hook)
	}
}

func (n *notifier) wait() {
	if n != nil {
		n.wg.Wait()
	}
}

// A retry after a timeout may repeat a delivery that did arrive, so every attempt carries the same ID.
func deliveryID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func (n *notifier) deliver(hook config.Notification, body []byte, id string) error {
	var last error
	for attempt := 1; attempt <= notifyAttempts; attempt++ {
		retry, err := n.post(hook, body, id)
		if err == nil {
			return nil
		}
		last = err
		if !retry {
			break
		}
		time.Sleep(time.Duration(attempt) * notifyRetryBackoff)
	}
	return last
}

func (n *notifier) post(hook config.Notification, body []byte, id string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "goff-studio")
	req.Header.Set(eventHeader, EventFlagChanged)
	req.Header.Set(deliveryHeader, id)
	if hook.Secret != "" {
		mac := hmac.New(sha256.New, []byte(hook.Secret))
		mac.Write(body)
		req.Header.Set(signatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := n.client.Do(req) //nolint:gosec // the URL is the operator's own notifications config, never request input
	if err != nil {
		return true, err
	}
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode < 300:
		return false, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return true, fmt.Errorf("receiver answered %s", resp.Status)
	default:
		return false, fmt.Errorf("receiver answered %s", resp.Status)
	}
}

func payload(format string, ev ChangeEvent) ([]byte, error) {
	if format == config.NotifySlack {
		return json.Marshal(map[string]string{"text": slackText(ev)})
	}
	return json.Marshal(ev)
}

func slackText(ev ChangeEvent) string {
	flag := "`" + slackEscape(ev.Flag) + "`"
	if ev.URL != "" {
		flag = "<" + ev.URL + "|" + slackEscape(ev.Flag) + ">"
	}
	who := ev.Actor.Name
	if who == "" {
		who = ev.Actor.Email
	}

	var b strings.Builder
	if ev.Team != "" {
		flag = slackEscape(ev.Team) + "/" + flag
	}
	fmt.Fprintf(&b, "*[%s]* %s: %s, by %s", slackEscape(ev.Environment), flag, slackEscape(ev.Summary), slackEscape(who))
	if ev.Reason != "" {
		b.WriteString("\n>Reason: " + slackEscape(ev.Reason))
	}
	if ev.Reference != "" {
		b.WriteString("\n>Reference: " + slackEscape(ev.Reference))
	}
	return b.String()
}

func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Webhook URLs usually embed their own credential, so only the host is ever logged.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "the configured URL"
	}
	return u.Scheme + "://" + u.Host
}
