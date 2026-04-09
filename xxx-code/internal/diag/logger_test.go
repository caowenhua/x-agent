package diag

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input   string
		want    Level
		wantErr bool
	}{
		{input: "", want: LevelInfo},
		{input: "info", want: LevelInfo},
		{input: "debug", want: LevelDebug},
		{input: "error", want: LevelError},
		{input: " noisy ", wantErr: true},
	}

	for _, test := range tests {
		got, err := ParseLevel(test.input)
		if test.wantErr {
			if err == nil {
				t.Fatalf("ParseLevel(%q) expected error", test.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseLevel(%q) returned error: %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", test.input, got, test.want)
		}
	}
}

func TestLoggerRespectsLevels(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, LevelInfo)

	logger.Debugf("hidden %d", 1)
	logger.Infof("shown %d", 2)
	logger.Errorf("failed %d", 3)

	text := output.String()
	if strings.Contains(text, "hidden 1") {
		t.Fatalf("expected debug message to be filtered out, got %q", text)
	}
	if !strings.Contains(text, "[INFO] shown 2") {
		t.Fatalf("expected info message in log output, got %q", text)
	}
	if !strings.Contains(text, "[ERROR] failed 3") {
		t.Fatalf("expected error message in log output, got %q", text)
	}
}

func TestNewTraceIDFormat(t *testing.T) {
	traceID := NewTraceID()
	if traceID == "trace_unknown" {
		t.Fatalf("expected generated trace id, got %q", traceID)
	}
	if !strings.HasPrefix(traceID, "trace_") {
		t.Fatalf("expected trace prefix, got %q", traceID)
	}
	if len(traceID) != len("trace_")+16 {
		t.Fatalf("expected 16 hex chars after prefix, got %q", traceID)
	}
}
