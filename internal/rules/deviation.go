package rules

// markDeviations flags each path cell whose value differs from the account's
// most common path for that check, so the report can point at the one
// repository that keeps its commitlint config somewhere unusual.
//
// The standard is the plurality: the value held by strictly more repositories
// than any other. A tie for most common, or a single distinct value, names no
// standard, because picking one of two equally common spellings would accuse
// half the account of deviating. An empty value is an absent file, which the
// check's verdict already reports, so it never deviates and is not counted.
func markDeviations(repos []RepoReport) {
	for _, c := range Checks() {
		if !c.PathCheck() {
			continue
		}
		std, ok := pluralityValue(repos, c)
		if !ok {
			continue
		}
		for i := range repos {
			cell, has := repos[i].Cells[c]
			if has && cell.Value != "" && cell.Value != std {
				cell.Deviates = true
				repos[i].Cells[c] = cell
			}
		}
	}
}

// pluralityValue returns the single most common non-empty value of a check
// across repos. It reports false when fewer than two distinct values exist or
// when the highest count is shared.
func pluralityValue(repos []RepoReport, c Check) (string, bool) {
	counts := make(map[string]int)
	for i := range repos {
		if v := repos[i].Cells[c].Value; v != "" {
			counts[v]++
		}
	}
	if len(counts) < 2 {
		return "", false
	}
	best, bestCount, tied := "", 0, false
	for v, n := range counts {
		switch {
		case n > bestCount:
			best, bestCount, tied = v, n, false
		case n == bestCount:
			tied = true
		}
	}
	return best, !tied
}
