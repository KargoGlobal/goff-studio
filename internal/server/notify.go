package server

import (
	"bytes"
	"context"
	"crypto/hmac"
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
	"github.com/go-feature-flag/studio/internal/storage"
)

const (
	EventFlagChanged = "flag.changed"
	notifyTimeout    = 10 * time.Second
	notifyAttempts   = 3
	signatureHeader  = "X-Studio-Signature"
	eventHeader      = "X-Studio-Event"
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
	baseURL string
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
		baseURL: strings.TrimRight(cfg.Server.BaseURL, "/"),
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
	result, err := b.Backend.Write(ctx, op, who)
	if err == nil && result != nil && result.Version != "" {
		b.notify.send(b.notify.event(op, who, result.Version, noteFrom(ctx)))
	}
	return result, err
}

func (n *notifier) event(op storage.ChangeOp, who storage.Identity, version string, note ChangeNote) ChangeEvent {
	environment, _, _ := strings.Cut(op.Path, "/")
	subject, _, _ := strings.Cut(op.Message, "\n")
	team, summary := teamNameOf(op.Path), subject
	if _, rest, ok := strings.Cut(subject, "] "); ok {
		if area, rest, ok := strings.Cut(rest, "/"); ok {
			team = area
			if _, s, ok := strings.Cut(rest, ": "); ok {
				summary = s
			}
		}
	}

	ev := ChangeEvent{
		Event:       EventFlagChanged,
		Environment: environment,
		Team:        team,
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
	if n.baseURL != "" && op.Key != "" {
		ev.URL = n.baseURL + "/env/" + url.PathEscape(environment) + "/flags/" + url.PathEscape(op.Key)
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
			if err := n.deliver(hook, body); err != nil {
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

func (n *notifier) deliver(hook config.Notification, body []byte) error {
	var last error
	for attempt := 1; attempt <= notifyAttempts; attempt++ {
		retry, err := n.post(hook, body)
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

func (n *notifier) post(hook config.Notification, body []byte) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "goff-studio")
	req.Header.Set(eventHeader, EventFlagChanged)
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
	fmt.Fprintf(&b, "*[%s]* %s/%s: %s, by %s", slackEscape(ev.Environment), slackEscape(ev.Team), flag, slackEscape(ev.Summary), slackEscape(who))
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
