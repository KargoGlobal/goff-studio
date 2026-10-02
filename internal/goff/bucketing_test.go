package goff

import (
	"strings"
	"testing"
)

const bucketedSrc = `# rollout flags
pacing:
  variations: {on: true, off: false}
  defaultRule:
    percentage: {on: 50, off: 50}
  bucketingKey: account.id # keep each account on one side
  metadata: {team: growth}
`

func TestBucketingKeyIsReadableAndNotPreserved(t *testing.T) {
	f := flagNamed(t, []byte(bucketedSrc), "pacing")
	if f.BucketingKey != "account.id" {
		t.Errorf("bucketingKey = %q", f.BucketingKey)
	}
	for _, p := range f.Preserved {
		if p == "bucketingKey" {
			t.Errorf("bucketingKey is editable, it must not be preserved: %v", f.Preserved)
		}
	}
}

func TestUnrelatedEditKeepsBucketingKeyByteForByte(t *testing.T) {
	src := []byte(bucketedSrc)
	f := flagNamed(t, src, "pacing")
	f.Enabled = false
	out, err := New().Serialize(src, "pacing", f)
	if err != nil {
		t.Fatal(err)
	}
	added, removed := lineDiff(string(src), string(out))
	if len(removed) != 0 || len(added) != 1 || strings.TrimSpace(added[0]) != "disable: true" {
		t.Errorf("want only the disable line added, got +%v -%v\n%s", added, removed, out)
	}
}

func TestChangingBucketingKeyTouchesOneLineAndKeepsItsComment(t *testing.T) {
	src := []byte(bucketedSrc)
	f := flagNamed(t, src, "pacing")
	f.BucketingKey = "region"
	out, err := New().Serialize(src, "pacing", f)
	if err != nil {
		t.Fatal(err)
	}
	added, removed := lineDiff(string(src), string(out))
	if len(added) != 1 || len(removed) != 1 {
		t.Fatalf("want one changed line, got +%v -%v", added, removed)
	}
	if strings.TrimSpace(added[0]) != "bucketingKey: region # keep each account on one side" {
		t.Errorf("added = %q", added[0])
	}
}

func TestBucketingKeyCanBeAddedAndCleared(t *testing.T) {
	src := []byte(`plain:
  variations: {on: true, off: false}
  defaultRule: {variation: "off"}
`)
	f := flagNamed(t, src, "plain")
	f.BucketingKey = "account.id"
	out, err := New().Serialize(src, "plain", f)
	if err != nil {
		t.Fatal(err)
	}
	if err := New().Validate(out); err != nil {
		t.Fatalf("GO Feature Flag rejects the output: %v\n%s", err, out)
	}
	added, removed := lineDiff(string(src), string(out))
	if len(removed) != 0 || len(added) != 1 || strings.TrimSpace(added[0]) != "bucketingKey: account.id" {
		t.Errorf("want only the bucketingKey line added, got +%v -%v", added, removed)
	}

	cleared := flagNamed(t, out, "plain")
	cleared.BucketingKey = ""
	back, err := New().Serialize(out, "plain", cleared)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != string(src) {
		t.Errorf("clearing should restore the original file:\n%s", back)
	}
}

func TestNewFlagWritesBucketingKey(t *testing.T) {
	out, err := New().Serialize(nil, "fresh", Flag{
		Enabled:      true,
		Variations:   []Variation{{Name: "on", Value: true}, {Name: "off", Value: false}},
		Default:      Outcome{Percentage: map[string]float64{"on": 10, "off": 90}},
		BucketingKey: "account.id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := flagNamed(t, out, "fresh").BucketingKey; got != "account.id" {
		t.Errorf("round trip lost bucketingKey: %q\n%s", got, out)
	}
}

func TestPreviewHonoursBucketingKey(t *testing.T) {
	src := []byte(`pacing:
  variations: {on: true, off: false}
  defaultRule:
    percentage: {on: 50, off: 50}
  bucketingKey: accountId
`)
	a := New()
	first := a.Evaluate(src, "pacing", "request-0", map[string]any{"accountId": "acct-1"})
	if first.Error != "" {
		t.Fatalf("evaluate: %s", first.Error)
	}
	for i := 1; i < 40; i++ {
		got := a.Evaluate(src, "pacing", itoa(i), map[string]any{"accountId": "acct-1"})
		if got.Variation != first.Variation {
			t.Fatalf("same account got %s and %s", first.Variation, got.Variation)
		}
	}
	missing := a.Evaluate(src, "pacing", "request-0", nil)
	if missing.Variation == "on" || missing.Variation == "off" {
		t.Errorf("a missing bucketing attribute must not land in the split, got %+v", missing)
	}
}
