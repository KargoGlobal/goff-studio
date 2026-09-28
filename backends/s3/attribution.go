package s3

import (
	"encoding/base64"
	"mime"
	"strings"
	"unicode/utf8"

	"github.com/go-feature-flag/studio/internal/storage"
)

// Attribution is stored as S3 user metadata on every object version Studio
// writes, so a versioned bucket keeps the same "who changed what" record that a
// Git commit would.
//
// S3 sends user metadata as x-amz-meta-* HTTP headers, which limits it to ASCII
// and to 2 KB in total (the UTF-8 bytes of every key plus every value). Values
// are therefore stored like this:
//
//   - printable ASCII (0x20-0x7E) with no "=?" sequence is stored verbatim, so
//     the common case stays readable in the S3 console and CLI;
//   - anything else (non-ASCII names, control characters) is stored as a single
//     RFC 2047 encoded word, "=?UTF-8?B?<base64>?=", the same form S3 itself
//     returns for non-ASCII metadata, and it is decoded on read;
//   - name, email and id are truncated to maxIdentityBytes each, and the change
//     message is truncated with "..." to whatever is left of the 2 KB budget.
const (
	metaUserName  = "studio-user-name"
	metaUserEmail = "studio-user-email"
	metaUserID    = "studio-user-id"
	metaMessage   = "studio-message"

	metadataLimit    = 2048
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

// metaValue reads a metadata key regardless of the case the SDK or an
// S3-compatible server hands back.
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
