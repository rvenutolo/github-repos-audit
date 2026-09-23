package rules

// identityLines builds the Git identities listing: one line per repository
// with any of the account holder's identities, in report order.
//
// It is built from every repository, whatever its verdict. An override or an
// informational type changes what the Policy table says about a repository,
// not what its history carries, and the listing is the owner's working list
// for rewriting that history. With no standard there is nothing to say whose
// identities are whose, so there is no listing.
func identityLines(repos []RepoReport, std *identityStandard) []IdentityLine {
	if std == nil {
		return nil
	}
	var out []IdentityLine
	for _, r := range repos {
		mine := std.mine(r.Repo.Identities)
		if len(mine) == 0 {
			continue
		}
		line := IdentityLine{
			Repo:       r.Repo.Name,
			Identities: make([]IdentityEntry, 0, len(mine)),
			Mixed:      len(mine) >= 2,
			Canonical:  true,
		}
		for _, id := range mine {
			canonical := sameIdentity(id, std.canonical)
			line.Identities = append(line.Identities, IdentityEntry{Name: id.Name, Email: id.Email, Canonical: canonical})
			line.Canonical = line.Canonical && canonical
		}
		out = append(out, line)
	}
	return out
}
