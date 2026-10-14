package auditschema_test

import (
	"bytes"
	"testing"

	auditschema "github.com/rvenutolo/github-repos-audit"
)

func TestJSON_returnsACopy(t *testing.T) {
	t.Parallel()

	a := auditschema.JSON()
	if len(a) == 0 {
		t.Fatal("JSON() is empty")
	}
	want := bytes.Clone(a)
	a[0] ^= 0xff
	if got := auditschema.JSON(); !bytes.Equal(got, want) {
		t.Error("altering the returned bytes changed what the next JSON() call returns")
	}
}
