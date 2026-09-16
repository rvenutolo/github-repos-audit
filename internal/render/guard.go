package render

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// volatileFields are the JSON keys that move on every single run whatever else
// is true, and are therefore excluded from the change guard's comparison. The
// list is a maintenance cost, paid deliberately: without it the guard can
// never fire, because the previous refresh's own merge advances this
// repository's pushed_at and every run would open a pull request over a
// timestamp.
//
// Keys are matched at any depth: pushed_at appears once per repository.
//
//nolint:gochecknoglobals // immutable lookup table
var volatileFields = map[string]bool{
	"generated_at":      true,
	"pushed_at":         true,
	"last_published_at": true,
	// head_oid is here for exactly the reason pushed_at is: this repository's
	// own head advances with every merge, the previous refresh's merge
	// included, so leaving it in means the guard can never fire and the cron
	// opens a pull request every night over a SHA. It says which commit the
	// verdicts describe, not what any of them is.
	"head_oid": true,
}

// MaterialChange reports whether anything that matters differs between the
// committed artefacts and the freshly rendered ones.
//
// The comparison is over verdicts, not clocks. audit.json is compared with
// every volatile field stripped, and the README with its footer line removed;
// what remains is what a reader would actually notice. Absent previous
// artefacts count as a change, which is how the first run produces a pull
// request.
func MaterialChange(oldJSON, newJSON []byte, oldREADME, newREADME string) (bool, error) {
	if len(oldJSON) == 0 || oldREADME == "" {
		return true, nil
	}

	before, err := stripVolatile(oldJSON)
	if err != nil {
		return false, fmt.Errorf("parse the committed audit.json: %w", err)
	}
	after, err := stripVolatile(newJSON)
	if err != nil {
		return false, fmt.Errorf("parse the rendered audit.json: %w", err)
	}
	// reflect.DeepEqual rather than cmp.Equal: go-cmp is a test-only
	// dependency here, and both sides are plain any trees out of
	// encoding/json, which DeepEqual compares exactly.
	if !reflect.DeepEqual(before, after) {
		return true, nil
	}

	// Only the generated block is compared: prose above the markers is not
	// this tool's output, and a change to it is not a reason to refresh.
	beforeBlock, err := GeneratedBlock(oldREADME)
	if err != nil {
		return false, fmt.Errorf("read the committed README: %w", err)
	}
	afterBlock, err := GeneratedBlock(newREADME)
	if err != nil {
		return false, fmt.Errorf("read the rendered README: %w", err)
	}
	return dropFooter(beforeBlock) != dropFooter(afterBlock), nil
}

// stripVolatile parses JSON and removes every volatile key at every depth, so
// the comparison is structural rather than a line filter. It decodes into any
// on purpose: the committed audit.json may predate a change to the snapshot's
// shape, and a generic decode still compares it where a concrete struct would
// either fail or silently drop the fields that differ.
func stripVolatile(data []byte) (any, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return strip(v), nil
}

func strip(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if volatileFields[k] {
				continue
			}
			out[k] = strip(val)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, val := range t {
			out = append(out, strip(val))
		}
		return out
	default:
		return v
	}
}

// dropFooter removes the "Read from GitHub on ..." line, which changes daily
// and says nothing about the account.
func dropFooter(block string) string {
	lines := strings.Split(block, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), footerPrefix) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
