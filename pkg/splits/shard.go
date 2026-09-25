package splits

import (
	"crypto/md5" //nolint:gosec // bucketing, not security; the algorithm is fixed for compatibility
	"encoding/binary"
)

// ShardOf returns uint32(big-endian first 4 bytes of MD5(salt + "-" + subject)) mod totalShards.
func ShardOf(salt, subject string, totalShards int) int {
	var buf [256]byte
	input := buf[:0]
	if n := len(salt) + 1 + len(subject); n > len(buf) {
		input = make([]byte, 0, n)
	}
	input = append(input, salt...)
	input = append(input, '-')
	input = append(input, subject...)
	sum := md5.Sum(input) //nolint:gosec // see import
	return int(int64(binary.BigEndian.Uint32(sum[:4])) % int64(totalShards))
}

func (s Shard) contains(value int) bool {
	for _, r := range s.Ranges {
		if value >= r.Start && value < r.End {
			return true
		}
	}
	return false
}

// Matches reports whether subject hashes into any of the shard's ranges.
func (s Shard) Matches(subject string, totalShards int) bool {
	return s.contains(ShardOf(s.Salt, subject, totalShards))
}

// Covered is the number of shard values the ranges include, counting overlaps once.
func (s Shard) Covered(totalShards int) int {
	n := 0
	for _, r := range normalizeRanges(s.Ranges, totalShards) {
		n += r.Len()
	}
	return n
}
