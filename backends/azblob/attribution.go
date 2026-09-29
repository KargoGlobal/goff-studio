package azblob

import (
	"encoding/base64"
	"mime"
	"strings"
	"unicode/utf8"

	"github.com/go-feature-flag/studio/internal/storage"
)

// Attribution is stored as blob metadata on every version Studio writes, so a
// storage account with blob versioning keeps the same "who changed what"
// record that a Git commit would.
//
// Azure sends metadata as x-ms-meta-* HTTP headers, so values are limited to
// ASCII and keys must be valid C# identifiers (hence underscores). All metadata
// together is limited to 8 KB. Values are stored like this:
//
//   - printable ASCII (0x20-0x7E) with no "=?" sequence is stored verbatim, so
//     the common case stays readable in the portal and CLI;
//   - anything else (non-ASCII names, control characters) is stored as a single
//     RFC 2047 encoded word, "=?UTF-8?B?<base64>?=", and decoded on read;
//   - name, email and id are truncated to maxIdentityBytes each, and the change
//     message is truncated with "..." to whatever is left of the 8 KB budget.
const (
	metaUserName  = "studio_user_name"
	metaUserEmail = "studio_user_email"
	metaUserID    = "studio_user_id"
	metaMessage   = "studio_message"

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

// attributionMetadata builds the user metadata for one write. It never exceeds
// metadataLimit and drops fields that are empty.
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
		encoded := encodeValue(truncate(field.value, maxIdentityBytes))
		meta[field.key] = encoded
		used += len(field.key) + len(encoded)
	}

	if message != "" {
		if encoded := fitMessage(message, metadataLimit-used-len(metaMessage)); encoded != "" {
			meta[metaMessage] = encoded
		}
	}

	if len(meta) == 0 {
		return nil
	}
	return meta
}

// fitMessage returns the encoded message, truncated with an ellipsis so that
// the encoded form is at most budget bytes.
func fitMessage(message string, budget int) string {
	if budget <= len(ellipsis) {
		return ""
	}
	if encoded := encodeValue(message); len(encoded) <= budget {
		return encoded
	}

	// The encoded form is never shorter than the raw bytes, so start from a
	// prefix that fits raw and shed runes until the encoded form fits too.
	cut := truncateBytes(message, budget-len(ellipsis))
	for cut != "" {
		if encoded := encodeValue(cut + ellipsis); len(encoded) <= budget {
			return encoded
		}
		_, size := utf8.DecodeLastRuneInString(cut)
		cut = cut[:len(cut)-size]
	}
	return ellipsis
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return truncateBytes(s, limit-len(ellipsis)) + ellipsis
}

// truncateBytes cuts s to at most limit bytes without splitting a rune.
func truncateBytes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for s != "" && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func encodeValue(v string) string {
	if isPlainASCII(v) {
		return v
	}
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(v)) + "?="
}

func isPlainASCII(v string) bool {
	if strings.Contains(v, "=?") || strings.TrimSpace(v) != v {
		return false
	}
	for i := range len(v) {
		if v[i] < 0x20 || v[i] > 0x7e {
			return false
		}
	}
	return true
}

var wordDecoder = &mime.WordDecoder{}

func decodeValue(v string) string {
	if !strings.Contains(v, "=?") {
		return v
	}
	decoded, err := wordDecoder.DecodeHeader(v)
	if err != nil {
		return v
	}
	return decoded
}

// metaValue reads a metadata key regardless of the case the SDK or the
// service hands back.
func metaValue(meta map[string]string, key string) string {
	if v, ok := meta[key]; ok {
		return decodeValue(v)
	}
	for k, v := range meta {
		if strings.EqualFold(k, key) {
			return decodeValue(v)
		}
	}
	return ""
}
