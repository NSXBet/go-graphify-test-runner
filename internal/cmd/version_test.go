package cmd

import (
	"bytes"
	"testing"

	"github.com/NSXBet/go-smart-test-runner/internal/version"
)

func TestVersionCommandPrintsInjectedVersion(t *testing.T) {
	version.Version = "v1.2.3"

	t.Cleanup(func() { version.Version = "" })

	cmd := newVersionCmd()

	var buf bytes.Buffer

	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if got := buf.String(); got != "v1.2.3\n" {
		t.Fatalf("version output = %q want %q", got, "v1.2.3\n")
	}
}

func TestVersionCommandDefaultsToDev(t *testing.T) {
	version.Version = ""

	cmd := newVersionCmd()

	var buf bytes.Buffer

	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if got := buf.String(); got != "dev\n" {
		t.Fatalf("version output = %q want dev", got)
	}
}

func TestRootHasVersionCommandAndFlag(t *testing.T) {
	root := newRootCmd()

	found := false

	for _, c := range root.Commands() {
		if c.Name() == "version" {
			found = true
		}
	}

	if !found {
		t.Fatal("version subcommand not registered")
	}

	if root.Version == "" {
		t.Fatal("root --version not set")
	}
}
