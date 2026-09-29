package gcs

import (
	"strings"
	"unicode/utf8"

	"github.com/go-feature-flag/studio/internal/storage"
)

// Attribution is stored as GCS custom metadata on every generation Studio
// writes, so a bucket with object versioning keeps the same "who changed what"
// record a Git commit would.
//
// The JSON API accepts UTF-8 metadata values, so unlike S3 nothing is encoded.
// Custom metadata is limited to 8 KiB in total (keys plus values); name, email
// and id are capped at maxIdentityBytes each, and the change message is
// truncated with "..." to whatever is left of metadataLimit.
const (
	metaUserName  = "studio-user-name"
	metaUserEmail = "studio-user-email"
	metaUserID    = "studio-user-id"
	metaMessage   = "studio-message"

	metadataLimit    = 8192
	maxIdentityBytes = 256
	ellipsis         = "..."
)

// changeMessage mirrors the message the github backend commits with.
func changeMessage(message, flagKey string) string {
	if strings.TrimSpace(message) != "" {
		return message
	}
	if flagKey != "" {
		return "update " + flagKey
	}
	return ""
}

func attributionMetadata(who storage.Identity, message string) map[string]string {
	meta := map[string]string{}
	used := 0

	for _, field := range []struct{ key, value string }{
		{metaUserName, who.Name},
		{metaUserEmail, who.Email},
		{metaUserID, who.Subject},
	} {
		if field.value == "" {
			continue
		}
		v := truncate(field.value, maxIdentityBytes)
		meta[field.key] = v
		used += len(field.key) + len(v)
	}

	if message != "" {
		if v := truncate(message, metadataLimit-used-len(metaMessage)); v != "" {
			meta[metaMessage] = v
		}
	}

	if len(meta) == 0 {
		return nil
	}
	return meta
}

// truncate cuts s to at most limit bytes, ending in an ellipsis when cut,
// without splitting a rune.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if limit <= len(ellipsis) {
		return ""
	}
	s = s[:limit-len(ellipsis)]
	for s != "" && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + ellipsis
}
