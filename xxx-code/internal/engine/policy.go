package engine

import (
	"fmt"
	"path/filepath"
	"strings"
)

type PermissionPolicy struct {
	ReadRoots   []string
	WriteRoots  []string
	ReadOnly    bool
	BashEnabled bool
}

func (r *Runner) EnsureReadPath(path string) error {
	return r.ensurePathAllowed(path, false)
}

func (r *Runner) EnsureWritePath(path string) error {
	return r.ensurePathAllowed(path, true)
}

func (r *Runner) EnsureBash(command string) error {
	if r == nil {
		return nil
	}
	if !r.config.PermissionPolicy.BashEnabled {
		return fmt.Errorf("bash tool is disabled by policy")
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is required")
	}
	return nil
}

func (r *Runner) PermissionPolicy() PermissionPolicy {
	policy := r.config.PermissionPolicy
	policy.ReadRoots = normalizeRoots(policy.ReadRoots)
	policy.WriteRoots = normalizeRoots(policy.WriteRoots)
	return policy
}

func (r *Runner) ensurePathAllowed(path string, write bool) error {
	if r == nil {
		return nil
	}

	policy := r.PermissionPolicy()
	target, err := normalizePath(path)
	if err != nil {
		return err
	}

	if write {
		if policy.ReadOnly {
			return fmt.Errorf("write access is disabled by read-only mode")
		}
		if len(policy.WriteRoots) == 0 {
			return nil
		}
		if pathWithinAnyRoot(target, policy.WriteRoots) {
			return nil
		}
		return fmt.Errorf("path %s is outside allowed write roots", target)
	}

	if len(policy.ReadRoots) == 0 {
		return nil
	}
	if pathWithinAnyRoot(target, policy.ReadRoots) {
		return nil
	}
	return fmt.Errorf("path %s is outside allowed read roots", target)
}

func normalizeRoots(roots []string) []string {
	normalized := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		path, err := normalizePath(root)
		if err != nil || path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	return normalized
}

func normalizePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	return abs, nil
}

func pathWithinAnyRoot(target string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, target)
		if err != nil {
			continue
		}
		if rel == "." {
			return true
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return true
	}
	return false
}
