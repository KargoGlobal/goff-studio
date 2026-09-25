package splits

import "sort"

// normalizeRanges clips to [0,totalShards), sorts, and merges touching or overlapping ranges.
func normalizeRanges(ranges []Range, totalShards int) []Range {
	out := make([]Range, 0, len(ranges))
	for _, r := range ranges {
		start, end := max(r.Start, 0), min(r.End, totalShards)
		if start < end {
			out = append(out, Range{start, end})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })

	merged := out[:0]
	for _, r := range out {
		if n := len(merged); n > 0 && r.Start <= merged[n-1].End {
			merged[n-1].End = max(merged[n-1].End, r.End)
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

func rangesIntersect(a, b []Range) bool {
	for _, x := range a {
		for _, y := range b {
			if x.Start < y.End && y.Start < x.End {
				return true
			}
		}
	}
	return false
}

// GrowRanges adds `extra` uncovered shard values after the last covered one,
// wrapping to 0, so every value already covered stays covered.
func GrowRanges(ranges []Range, extra, totalShards int) []Range {
	current := normalizeRanges(ranges, totalShards)
	if extra <= 0 {
		return current
	}

	cursor := 0
	if n := len(current); n > 0 {
		cursor = current[n-1].End % totalShards
	}

	covered := func(v int) (bool, int) {
		for _, r := range current {
			if v >= r.Start && v < r.End {
				return true, r.End
			}
		}
		return false, 0
	}

	added := make([]Range, 0, 2)
	for scanned := 0; extra > 0 && scanned < totalShards; {
		if in, end := covered(cursor); in {
			scanned += end - cursor
			cursor = end % totalShards
			continue
		}
		next := totalShards
		for _, r := range current {
			if r.Start > cursor && r.Start < next {
				next = r.Start
			}
		}
		take := min(next-cursor, extra)
		added = append(added, Range{cursor, cursor + take})
		extra -= take
		scanned += take
		cursor = (cursor + take) % totalShards
	}
	return normalizeRanges(append(current, added...), totalShards)
}
