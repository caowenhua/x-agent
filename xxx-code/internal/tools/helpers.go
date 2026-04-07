package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/caowenhua/x-agent/xxx-code/internal/engine"
)

func resolvePath(cwd, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("path is required")
	}
	var path string
	if filepath.IsAbs(raw) {
		path = raw
	} else {
		path = filepath.Join(cwd, raw)
	}
	return filepath.Abs(filepath.Clean(path))
}

func mustJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(data)
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func ensureReadAllowed(execCtx *engine.ExecutionContext, path string) error {
	if execCtx == nil {
		return nil
	}
	return execCtx.EnsureReadPath(path)
}

func ensureWriteAllowed(execCtx *engine.ExecutionContext, path string) error {
	if execCtx == nil {
		return nil
	}
	return execCtx.EnsureWritePath(path)
}
