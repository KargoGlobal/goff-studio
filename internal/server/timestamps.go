package server

import (
	"time"

	"github.com/go-feature-flag/studio/internal/goff"
)

const (
	metaCreatedAt = "createdAt"
	metaUpdatedAt = "updatedAt"
)

// Metadata keys Studio owns, which promotion never copies and compare never reports as drift.
var studioMetadata = map[string]bool{"team": true, metaCreatedAt: true, metaUpdatedAt: true}

func stamp(f *goff.Flag, now time.Time, created bool) {
	at := now.UTC().Truncate(time.Second).Format(time.RFC3339)
	meta := make(map[string]any, len(f.Metadata)+2)
	for k, v := range f.Metadata {
		meta[k] = v
	}
	if created {
		meta[metaCreatedAt] = at
	}
	meta[metaUpdatedAt] = at
	f.Metadata = meta
}
