package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caowenhua/x-agent/xxx-code/internal/config"
)

func TestOpenErrorOutputDefaultsToStderr(t *testing.T) {
	writer, closeFn, err := openErrorOutput(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if writer != os.Stderr {
		t.Fatalf("expected stderr writer, got %T", writer)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("expected noop close function, got %v", err)
	}
}

func TestOpenErrorOutputCreatesAndWritesLogFile(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "logs", "xxx-code.log")

	oldStderr := os.Stderr
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writePipe
	defer func() {
		os.Stderr = oldStderr
		_ = readPipe.Close()
	}()

	writer, closeFn, err := openErrorOutput(config.Config{LogFile: logFile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, "hello log\n"); err != nil {
		t.Fatal(err)
	}
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatal(err)
	}

	stderrData, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stderrData), "hello log") {
		t.Fatalf("expected mirrored stderr output, got %q", string(stderrData))
	}

	fileData, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fileData), "hello log") {
		t.Fatalf("expected log file to contain written text, got %q", string(fileData))
	}
}
