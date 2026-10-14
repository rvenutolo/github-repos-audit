// Package schema validates an audit.json document against the JSON Schema the
// binary embeds, so a report repository can check the file it commits with
// this tool alone.
package schema

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	auditschema "github.com/rvenutolo/github-repos-audit"
)

// compiled holds the schema, compiled on first use. A failure to compile the
// embedded schema is a defect in this binary, returned from Validate as an
// error rather than a panic so a caller can still report it.
var compiled = sync.OnceValues(compile) //nolint:gochecknoglobals // compiling is costly and the result is immutable; sync.OnceValues is the guarded cache

// parseEmbedded decodes the embedded schema and reads its $id, the URL the
// schema is registered under and compiled from. The embedded schema is a
// tracked file of this repository that the package's own tests compile, so a
// failure here is a broken build of this repository, not an input a caller
// can supply; the failure branches exist so such a build reports itself
// rather than panicking.
func parseEmbedded() (url string, doc any, err error) {
	doc, err = jsonschema.UnmarshalJSON(bytes.NewReader(auditschema.JSON()))
	if err != nil {
		// coverage-exempt: the embedded schema is a tracked file the package tests compile; a parse failure is a broken build, not an input
		return "", nil, fmt.Errorf("parse embedded schema: %w", err)
	}
	obj, isObject := doc.(map[string]any)
	if isObject {
		if id, isString := obj["$id"].(string); isString {
			url = id
		}
	}
	if url == "" {
		// coverage-exempt: the embedded schema is a tracked file the package tests compile; a missing $id is a broken build, not an input
		return "", nil, errors.New("embedded schema has no $id")
	}
	return url, doc, nil
}

// compile registers the embedded schema under its own $id and compiles that
// URL. No URL loader is installed: validation must never reach the network,
// so a reference the embedded document does not satisfy is an error, not a
// fetch. Format assertion is on, because the schema relies on formats such as
// date-time that are annotations only by default.
func compile() (*jsonschema.Schema, error) {
	url, doc, err := parseEmbedded()
	if err != nil {
		// coverage-exempt: see parseEmbedded
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource(url, doc); err != nil {
		// coverage-exempt: the resource is registered once, under a fresh compiler; a failure is a broken embedded schema, not an input
		return nil, fmt.Errorf("register embedded schema: %w", err)
	}
	s, err := c.Compile(url)
	if err != nil {
		// coverage-exempt: a failure is a broken embedded schema, not an input; every Validate test that gets past this point compiled it
		return nil, fmt.Errorf("compile embedded schema: %w", err)
	}
	return s, nil
}

// Validate reports whether doc is JSON that satisfies the audit.json schema.
// It returns nil when it does. Otherwise the error lists every violation, one
// per line as "<JSON pointer>: <what is wrong>", sorted and without
// duplicates so the same document always yields the same text. The root of
// the document is written "(root)", because "/" is the pointer of the key "".
// An unknown key is reported once per key, at "<object pointer>/<key>".
//
// Duplicate object keys follow encoding/json: the last one wins, so a
// document that repeats a key is judged by its final value.
func Validate(doc []byte) error {
	s, err := compiled()
	if err != nil {
		// coverage-exempt: see parseEmbedded
		return err
	}
	if len(bytes.TrimSpace(doc)) == 0 {
		return errors.New("document is empty")
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	err = s.Validate(inst)
	if err == nil {
		return nil
	}
	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		// coverage-exempt: validating a decoded instance against a compiled schema
		// fails only with a *ValidationError; this keeps any other error visible.
		return err
	}
	var lines []string
	lines = collect(lines, verr, message.NewPrinter(language.English))
	slices.Sort(lines)
	return errors.New(strings.Join(slices.Compact(lines), "\n"))
}

// collect appends one line per leaf violation under node. Interior nodes only
// say that a subschema failed ("validation failed" for a $ref), which is not
// a violation, so only leaves are reported. Lines are built here rather than
// taken from the library's output because the library lists the unknown keys
// of an object in map-iteration order, which would make the text differ from
// run to run. The other kinds the schema uses render values from ordered
// slices (required names in schema order, types in a fixed order, enum values
// in schema order), so their library message is stable.
func collect(lines []string, node *jsonschema.ValidationError, p *message.Printer) []string {
	if len(node.Causes) > 0 {
		for _, cause := range node.Causes {
			lines = collect(lines, cause, p)
		}
		return lines
	}
	if extra, isExtra := node.ErrorKind.(*kind.AdditionalProperties); isExtra {
		for _, key := range extra.Properties {
			lines = append(lines, pointer(append(slices.Clone(node.InstanceLocation), key))+": unknown key")
		}
		return lines
	}
	return append(lines, pointer(node.InstanceLocation)+": "+node.ErrorKind.LocalizedString(p))
}

// pointer renders an instance location as an RFC 6901 JSON pointer, with the
// document root named "(root)" because the empty pointer would print as
// nothing.
func pointer(location []string) string {
	if len(location) == 0 {
		return "(root)"
	}
	var sb strings.Builder
	for _, part := range location {
		sb.WriteByte('/')
		sb.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(part))
	}
	return sb.String()
}
