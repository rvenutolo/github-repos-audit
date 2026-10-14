// Package auditschema embeds audit.schema.json so the binary can validate an
// audit.json without the file beside it. The schema stays at the repository
// root, where its $id and the README point, and go:embed cannot reach a parent
// directory, so the embedding has to live in a root-level package.
package auditschema

import (
	"bytes"
	_ "embed" // go:embed needs the package imported for its side effect
)

//go:embed audit.schema.json
var schemaJSON []byte

// JSON returns a copy of the embedded audit.schema.json. The copy keeps a
// caller from altering the bytes every later caller sees.
func JSON() []byte {
	return bytes.Clone(schemaJSON)
}
