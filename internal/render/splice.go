package render

import (
	"errors"
	"fmt"
	"strings"
)

// The markers that bracket the generated block. Everything above the first and
// below the second is hand-written prose the tool never touches.
const (
	// BeginMarker opens the generated block.
	BeginMarker = "<!-- BEGIN GENERATED: audit -->"
	// EndMarker closes it.
	EndMarker = "<!-- END GENERATED: audit -->"
)

// ErrMarkers reports a README the tool cannot safely rewrite, or a block that
// would make the result unreadable because it carries a marker of its own. A
// missing or unclosed marker is a hard error rather than a reason to append:
// guessing where the block belongs would either duplicate the report or
// overwrite prose somebody wrote.
var ErrMarkers = errors.New("generated markers")

// Splice replaces the generated block in readme with block, leaving every byte
// outside the markers untouched. The block must not contain either marker;
// one that does is refused with ErrMarkers, since writing it would produce a
// README the next run cannot read.
func Splice(readme, block string) (string, error) {
	if strings.Contains(block, BeginMarker) || strings.Contains(block, EndMarker) {
		return "", fmt.Errorf("%w: block contains a marker", ErrMarkers)
	}

	begin, end, err := locateMarkers(readme)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(readme[:begin])
	b.WriteString(BeginMarker)
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(block))
	b.WriteString("\n\n")
	b.WriteString(readme[end:])
	return b.String(), nil
}

// GeneratedBlock returns just the text between the markers, which is what the
// change guard compares. Every layout Splice refuses is an error here too: a
// README whose markers do not bracket exactly one block has no block to
// return, and picking one would make the guard compare text chosen by
// guesswork.
func GeneratedBlock(readme string) (string, error) {
	begin, end, err := locateMarkers(readme)
	if err != nil {
		return "", err
	}
	return readme[begin+len(BeginMarker) : end], nil
}

// locateMarkers returns the offsets of the two markers, refusing every layout
// in which they do not bracket exactly one block. Splice and GeneratedBlock
// share it so the writer and the reader agree on which READMEs are legible:
// one that reads a shape the other will not rewrite would compare a block
// that cannot be regenerated.
func locateMarkers(readme string) (begin, end int, err error) {
	begin = strings.Index(readme, BeginMarker)
	if begin < 0 {
		return 0, 0, fmt.Errorf("%w: %s is missing", ErrMarkers, BeginMarker)
	}
	if strings.Contains(readme[begin+len(BeginMarker):], BeginMarker) {
		return 0, 0, fmt.Errorf("%w: %s appears more than once", ErrMarkers, BeginMarker)
	}

	end = strings.Index(readme[begin:], EndMarker)
	if end < 0 {
		return 0, 0, fmt.Errorf("%w: %s is missing or precedes %s", ErrMarkers, EndMarker, BeginMarker)
	}
	end += begin
	// Look on both sides of the block: a stray closing marker before it would
	// otherwise survive as prose and be picked up on the next read.
	if strings.Contains(readme[:begin], EndMarker) || strings.Contains(readme[end+len(EndMarker):], EndMarker) {
		return 0, 0, fmt.Errorf("%w: %s appears more than once", ErrMarkers, EndMarker)
	}
	return begin, end, nil
}
