package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpDoesNotStartTUI(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--help"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "gitfleet [paths...]") {
		t.Fatal("missing usage")
	}
}
