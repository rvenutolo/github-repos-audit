// Package render turns a report into the markdown that lives between the
// README's generated markers, and writes it beside audit.json.
//
// It pads its own table columns. The old bash tool emitted unpadded tables and
// let prettier align them, which was safe because a human ran the formatter
// before committing. Here the cron commits `audit render` output directly, so
// unpadded output would land on the default branch and fail the repository's
// own format check every single day.
package render

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// bareURL matches a URL written as plain text.
var bareURL = regexp.MustCompile(`https?://[^\s<>` + "`" + `|]+`)

// table accumulates rows and renders them padded to prettier's own shape: one
// space inside each pipe, every column as wide as its widest cell, and a
// separator row of dashes filling that width.
type table struct {
	header []string
	rows   [][]string
}

func newTable(header ...string) *table { return &table{header: header} }

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

// String renders the table. An empty table renders as nothing at all, so a
// caller never emits a header over no rows.
func (t *table) String() string {
	if len(t.rows) == 0 {
		return ""
	}

	widths := make([]int, len(t.header))
	for i, h := range t.header {
		widths[i] = width(h)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i < len(widths) && width(cell) > widths[i] {
				widths[i] = width(cell)
			}
		}
	}

	var b strings.Builder
	writeRow(&b, t.header, widths)
	writeSeparator(&b, widths)
	for _, row := range t.rows {
		writeRow(&b, row, widths)
	}
	return b.String()
}

func writeRow(b *strings.Builder, cells []string, widths []int) {
	b.WriteString("|")
	for i, w := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		b.WriteString(" ")
		b.WriteString(cell)
		b.WriteString(strings.Repeat(" ", w-width(cell)))
		b.WriteString(" |")
	}
	b.WriteString("\n")
}

func writeSeparator(b *strings.Builder, widths []int) {
	b.WriteString("|")
	for _, w := range widths {
		b.WriteString(" ")
		b.WriteString(strings.Repeat("-", w))
		b.WriteString(" |")
	}
	b.WriteString("\n")
}

// width is the display width of a table cell. Every character the renderer
// emits is single-width — the tick and cross included — so counting runes is
// right and counting bytes would over-pad by two per tick.
func width(s string) int { return utf8.RuneCountInString(s) }

// escape makes a string safe inside a table cell.
//
// A repository description is data read from GitHub, not markup, and it has to
// survive being dropped into a table without changing what the table means. A
// pipe would end the cell, a newline would end the row, and a bare URL — which
// a real description can genuinely carry — is an MD034 failure that would red
// the gate on a file no human wrote. Backticks rather than angle brackets, so
// the URL stays text: a link to a third-party host would put the gate at the
// mercy of that host being up.
func escape(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	s = bareURL.ReplaceAllString(s, "`$0`")
	return strings.TrimSpace(s)
}

// code wraps a value in backticks for a table cell, escaping any backtick it
// contains by widening the fence, which is what markdown requires.
func code(s string) string {
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "`") {
		return "`" + s + "`"
	}
	fence := "``"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	return fence + " " + s + " " + fence
}
