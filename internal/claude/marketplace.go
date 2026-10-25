package claude

import (
	"encoding/json"
	"fmt"
)

// Marketplace is what a marketplace.json says, reduced to the facts the
// marketplace checks need.
type Marketplace struct {
	Entries    []MarketplaceEntry // every plugins entry, in the file's order
	Unreadable string
}

// MarketplaceEntry is one plugins entry.
type MarketplaceEntry struct {
	Name       string
	Repo       string // a github source's owner/name as written; empty for any other source
	Ref, SHA   string // a github source's pin, empty when absent
	HasVersion bool   // the entry carries a version key
}

// ParseMarketplace reads marketplace.json text. The shape follows
// https://code.claude.com/docs/en/plugin-marketplaces: a plugins array whose
// every entry needs a name and a source, the source being a relative path
// string or an object whose "source" key names its type. Only a "github"
// source names a repository this tool can look up, so every other source
// records an empty Repo.
//
// An entry Claude Code would refuse makes the whole file unreadable, so no
// marketplace check judges the entries that happened to parse.
func ParseMarketplace(text string) Marketplace {
	bad := func(format string, args ...any) Marketplace {
		return Marketplace{Unreadable: fmt.Sprintf(format, args...)}
	}
	var doc any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return bad("not valid JSON: %v", err)
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return bad("marketplace.json is not a JSON object")
	}
	plugins, ok := obj["plugins"].([]any)
	if !ok {
		return bad("plugins is not a list")
	}

	var m Marketplace
	for i, item := range plugins {
		entry, ok := item.(map[string]any)
		if !ok {
			return bad("plugins[%d] is not an object", i)
		}
		name, ok := entry["name"].(string)
		if !ok || name == "" {
			return bad("plugins[%d] has no name", i)
		}
		e := MarketplaceEntry{Name: name}
		_, e.HasVersion = entry["version"]
		switch source := entry["source"].(type) {
		case string:
			// A path inside the marketplace repository: no repository of its
			// own to look up.
		case map[string]any:
			if source["source"] == "github" {
				for _, f := range []struct {
					key string
					to  *string
				}{{"repo", &e.Repo}, {"ref", &e.Ref}, {"sha", &e.SHA}} {
					v, present := source[f.key]
					if !present {
						continue
					}
					s, ok := v.(string)
					if !ok {
						return bad("plugins[%d].source.%s is not a string", i, f.key)
					}
					*f.to = s
				}
			}
		default:
			return bad("plugins[%d] has no source", i)
		}
		m.Entries = append(m.Entries, e)
	}
	return m
}
