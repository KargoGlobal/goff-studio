package s3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-feature-flag/studio/internal/storage"
)

var jane = storage.Identity{Name: "Jane Doe", Email: "jane@example.com", Subject: "idp|00u1abc"}

func metadataSize(meta map[string]string) int {
	n := 0
	for k, v := range meta {
		n += len(k) + len(v)
	}
	return n
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7e || s[i] < 0x20 {
			return false
		}
	}
	return true
}

func appendDisable(current []byte) ([]byte, error) {
	return append(current, []byte("  disable: true\n")...), nil
}

func TestWriteStoresAttributionMetadata(t *testing.T) {
	f := newFake()
	b := backend(t, f)

	_, err := b.Write(context.Background(), storage.ChangeOp{
		Path:    "production/flags.goff.yaml",
		Key:     "flag-one",
		Message: "[production] growth/new-checkout: enabled",
		Apply:   appendDisable,
	}, jane)
	if err != nil {
		t.Fatal(err)
	}

	meta := f.lastMeta["production/flags.goff.yaml"]
	want := map[string]string{
		"studio-user-name":  "Jane Doe",
		"studio-user-email": "jane@example.com",
		"studio-user-id":    "idp|00u1abc",
		"studio-message":    "[production] growth/new-checkout: enabled",
	}
	for k, v := range want {
		if meta[k] != v {
			t.Errorf("metadata %s = %q, want %q", k, meta[k], v)
		}
	}
}

func TestWriteWithoutMessageFallsBackLikeGitHub(t *testing.T) {
	f := newFake()
	b := backend(t, f)

	_, err := b.Write(context.Background(), storage.ChangeOp{
		Path: "production/flags.goff.yaml", Key: "flag-one", Apply: appendDisable,
	}, storage.Identity{Email: "jane@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	meta := f.lastMeta["production/flags.goff.yaml"]
	if meta["studio-message"] != "update flag-one" {
		t.Errorf("message = %q", meta["studio-message"])
	}
	if _, ok := meta["studio-user-name"]; ok {
		t.Error("empty identity fields should be omitted, not stored blank")
	}
}

func TestCreateFileStoresAttributionMetadata(t *testing.T) {
	f := newFake()
	b := backend(t, f)

	if err := b.CreateFile(context.Background(), "dev/flags.goff.yaml", []byte("# new\n"), "create team growth", jane); err != nil {
		t.Fatal(err)
	}
	meta := f.lastMeta["dev/flags.goff.yaml"]
	if meta["studio-user-email"] != "jane@example.com" || meta["studio-message"] != "create team growth" {
		t.Errorf("metadata = %v", meta)
	}
}

func TestNonASCIIValuesAreEncodedAndRoundTrip(t *testing.T) {
	who := storage.Identity{Name: "Zoë Łukasiewicz \u674e\u96f7", Email: "zoe@example.com", Subject: "sub"}
	msg := "[production] growth/new-checkout: enabled — ✓"
	meta := attributionMetadata(who, msg)

	for k, v := range meta {
		if !isASCII(v) {
			t.Errorf("metadata %s must be ASCII, got %q", k, v)
		}
	}
	if !strings.HasPrefix(meta[metaUserName], "=?UTF-8?B?") {
		t.Errorf("non-ASCII name should be an RFC 2047 encoded word, got %q", meta[metaUserName])
	}
	if metaValue(meta, metaUserName) != who.Name {
		t.Errorf("name did not round-trip: %q", metaValue(meta, metaUserName))
	}
	if metaValue(meta, metaMessage) != msg {
		t.Errorf("message did not round-trip: %q", metaValue(meta, metaMessage))
	}
}

func TestASCIIThatLooksEncodedIsEncodedToo(t *testing.T) {
	for _, v := range []string{"=?UTF-8?B?Zm9v?=", " padded ", "line\nbreak"} {
		enc := encodeValue(v)
		if enc == v {
			t.Errorf("%q must be encoded so it reads back unchanged", v)
		}
		if got := decodeValue(enc); got != v {
			t.Errorf("round-trip of %q gave %q", v, got)
		}
	}
}

func TestLongMessagesAreTruncatedWithinTheLimit(t *testing.T) {
	for _, msg := range []string{
		strings.Repeat("a", 5000),
		strings.Repeat("é", 3000),
		strings.Repeat("\u674e", 2000),
	} {
		who := storage.Identity{Name: strings.Repeat("N", 1000), Email: strings.Repeat("e", 1000), Subject: strings.Repeat("ü", 1000)}
		meta := attributionMetadata(who, msg)

		if size := metadataSize(meta); size > metadataLimit {
			t.Errorf("metadata is %d bytes, over the %d limit", size, metadataLimit)
		}
		for k, v := range meta {
			if !isASCII(v) {
				t.Errorf("metadata %s must be ASCII", k)
			}
		}
		got := metaValue(meta, metaMessage)
		if !strings.HasSuffix(got, "...") {
			t.Errorf("a truncated message should end with an ellipsis, got suffix %q", got[len(got)-10:])
		}
		if !strings.HasPrefix(msg, strings.TrimSuffix(got, "...")) {
			t.Error("truncation must keep a clean prefix of the message")
		}
		if name := metaValue(meta, metaUserName); len(name) > maxIdentityBytes {
			t.Errorf("name is %d bytes, over %d", len(name), maxIdentityBytes)
		}
	}
}

func TestShortMessageIsNotTruncated(t *testing.T) {
	meta := attributionMetadata(jane, "short")
	if meta[metaMessage] != "short" {
		t.Errorf("message = %q", meta[metaMessage])
	}
}

func versionedBackend(t *testing.T) (*fakeS3, *Backend) {
	t.Helper()
	f := newFake()
	f.versioning = true
	return f, backend(t, f)
}

func TestHistoryReadsAttributionFromMetadata(t *testing.T) {
	_, b := versionedBackend(t)
	ctx := context.Background()
	path := "production/flags.goff.yaml"
	zoe := storage.Identity{Name: "Zoë", Email: "zoe@example.com"}

	for i, who := range []storage.Identity{jane, zoe} {
		_, err := b.Write(ctx, storage.ChangeOp{
			Path:    path,
			Key:     "flag-one",
			Message: fmt.Sprintf("[production] flags/flag-one: change %d", i+1),
			Apply:   func(c []byte) ([]byte, error) { return append(c, '#', byte('a'+i), '\n'), nil },
		}, who)
		if err != nil {
			t.Fatal(err)
		}
	}

	commits, err := b.History(ctx, path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %+v", commits)
	}
	if commits[0].SHA != "v2" || commits[0].Author != "Zoë" || commits[0].Email != "zoe@example.com" ||
		commits[0].Message != "[production] flags/flag-one: change 2" {
		t.Errorf("newest commit = %+v", commits[0])
	}
	if commits[1].Author != "Jane Doe" || commits[1].Email != "jane@example.com" {
		t.Errorf("older commit = %+v", commits[1])
	}
	if commits[0].When.IsZero() {
		t.Error("the version time should be kept")
	}
}

func TestHistoryFallsBackForVersionsWithoutMetadata(t *testing.T) {
	f, b := versionedBackend(t)
	path := "production/flags.goff.yaml"
	f.versions = map[string][]fakeVersion{path: {
		{id: "old", when: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{id: "new", when: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), meta: attributionMetadata(jane, "[production] x: enabled")},
	}}
	f.headErr = map[string]error{"broken": errors.New("access denied")}

	commits, err := b.History(context.Background(), path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %+v", commits)
	}
	if commits[0].Author != "Jane Doe" {
		t.Errorf("attributed version = %+v", commits[0])
	}
	old := commits[1]
	if old.Author != unknownAuthor || old.Email != "" || old.Message != "object version old" {
		t.Errorf("a version written before attribution should fall back, got %+v", old)
	}
}

func TestHistoryToleratesHeadFailures(t *testing.T) {
	f, b := versionedBackend(t)
	path := "production/flags.goff.yaml"
	f.versions = map[string][]fakeVersion{path: {{id: "broken", when: time.Now()}}}
	f.headErr = map[string]error{"broken": errors.New("access denied")}

	commits, err := b.History(context.Background(), path, 10)
	if err != nil {
		t.Fatalf("one unreadable version must not break history: %v", err)
	}
	if len(commits) != 1 || commits[0].Author != unknownAuthor {
		t.Errorf("commits = %+v", commits)
	}
}

func TestHistoryShowsDeleteMarkersWithoutAuthorOrHead(t *testing.T) {
	f, b := versionedBackend(t)
	path := "production/flags.goff.yaml"
	f.versions = map[string][]fakeVersion{path: {
		{id: "v1", when: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), meta: attributionMetadata(jane, "created")},
		{id: "d1", when: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), deleted: true},
	}}

	commits, err := b.History(context.Background(), path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || commits[0].SHA != "d1" {
		t.Fatalf("the delete marker should be the newest entry: %+v", commits)
	}
	if commits[0].Author != unknownAuthor || !strings.Contains(commits[0].Message, "deleted") {
		t.Errorf("delete marker = %+v", commits[0])
	}
	if f.heads != 1 {
		t.Errorf("delete markers have no metadata, so they must not be HEADed; heads = %d", f.heads)
	}
}

func TestHistoryHonoursLimitAndCapsConcurrency(t *testing.T) {
	f, b := versionedBackend(t)
	path := "production/flags.goff.yaml"
	var versions []fakeVersion
	for i := 0; i < 40; i++ {
		versions = append(versions, fakeVersion{
			id:   fmt.Sprintf("v%02d", i),
			when: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC),
			meta: attributionMetadata(jane, fmt.Sprintf("change %d", i)),
		})
	}
	f.versions = map[string][]fakeVersion{path: versions}

	commits, err := b.History(context.Background(), path, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 12 || commits[0].SHA != "v39" {
		t.Fatalf("got %d commits, first %+v", len(commits), commits[0])
	}
	if f.heads != 12 {
		t.Errorf("heads = %d, want one per returned version (12)", f.heads)
	}
	if peak := f.maxInFlight.Load(); peak > headConcurrency {
		t.Errorf("%d HeadObject calls in flight, cap is %d", peak, headConcurrency)
	}
}
