package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunMainHelperPluginEcho(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runMain([]string{"--helper-plugin-echo"}, strings.NewReader(`{"value":"hello"}`), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected helper mode to succeed, got %d", exitCode)
	}
	if stdout.String() != `{"value":"hello"}` {
		t.Fatalf("unexpected helper stdout: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunMainPrintsVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runMain([]string{"--version"}, nil, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected version mode to succeed, got %d", exitCode)
	}
	if !strings.Contains(stdout.String(), "xxx-code ") {
		t.Fatalf("expected build info output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}
