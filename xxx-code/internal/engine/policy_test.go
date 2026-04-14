package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPermissionPolicyReadWriteAndToolChecks(t *testing.T) {
	dir := t.TempDir()
	writeRoot := filepath.Join(dir, "workspace")
	otherDir := t.TempDir()

	runner := NewRunner(nil, NewRegistry(), RunnerConfig{
		WorkingDir: dir,
		PermissionPolicy: PermissionPolicy{
			ReadRoots:           []string{"", writeRoot, writeRoot},
			WriteRoots:          []string{writeRoot, writeRoot},
			AllowedTools:        []string{"bash", "read_file", "bash"},
			BlockedTools:        []string{"edit_file", "edit_file"},
			BashAllowedPrefixes: []string{"go env", "go test", "go env"},
			BashBlockedPrefixes: []string{"rm ", "sudo ", "rm "},
			BashEnabled:         true,
		},
	})

	policy := runner.PermissionPolicy()
	if len(policy.ReadRoots) != 1 || policy.ReadRoots[0] != writeRoot {
		t.Fatalf("expected deduplicated read roots, got %+v", policy.ReadRoots)
	}
	if len(policy.WriteRoots) != 1 || policy.WriteRoots[0] != writeRoot {
		t.Fatalf("expected deduplicated write roots, got %+v", policy.WriteRoots)
	}
	if len(policy.AllowedTools) != 2 || policy.AllowedTools[0] != "bash" || policy.AllowedTools[1] != "read_file" {
		t.Fatalf("expected normalized allowed tools, got %+v", policy.AllowedTools)
	}
	if len(policy.BlockedTools) != 1 || policy.BlockedTools[0] != "edit_file" {
		t.Fatalf("expected normalized blocked tools, got %+v", policy.BlockedTools)
	}

	inside := filepath.Join(writeRoot, "nested", "demo.txt")
	outside := filepath.Join(otherDir, "secret.txt")

	if err := runner.EnsureReadPath(inside); err != nil {
		t.Fatalf("expected inside read path to be allowed, got %v", err)
	}
	if err := runner.EnsureWritePath(inside); err != nil {
		t.Fatalf("expected inside write path to be allowed, got %v", err)
	}
	if err := runner.EnsureReadPath(outside); err == nil || !strings.Contains(err.Error(), "outside allowed read roots") {
		t.Fatalf("expected outside read path to be blocked, got %v", err)
	}
	if err := runner.EnsureWritePath(outside); err == nil || !strings.Contains(err.Error(), "outside allowed write roots") {
		t.Fatalf("expected outside write path to be blocked, got %v", err)
	}

	if err := runner.EnsureTool("bash"); err != nil {
		t.Fatalf("expected allowed tool to pass, got %v", err)
	}
	if err := runner.EnsureTool("edit_file"); err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("expected blocked tool error, got %v", err)
	}
	if err := runner.EnsureTool("write_file"); err == nil || !strings.Contains(err.Error(), "allowed tool list") {
		t.Fatalf("expected allowed-list error, got %v", err)
	}
	if err := runner.EnsureTool(""); err == nil || !strings.Contains(err.Error(), "tool name is required") {
		t.Fatalf("expected empty tool name error, got %v", err)
	}

	execCtx := &ExecutionContext{Runner: runner}
	if err := execCtx.EnsureReadPath(inside); err != nil {
		t.Fatalf("expected execution context read wrapper to pass, got %v", err)
	}
	if err := execCtx.EnsureWritePath(inside); err != nil {
		t.Fatalf("expected execution context write wrapper to pass, got %v", err)
	}
	if err := execCtx.EnsureBash("go env GOROOT"); err != nil {
		t.Fatalf("expected execution context bash wrapper to pass, got %v", err)
	}
}

func TestPermissionPolicyBashChecks(t *testing.T) {
	dir := t.TempDir()

	disabledRunner := NewRunner(nil, NewRegistry(), RunnerConfig{
		WorkingDir: dir,
		PermissionPolicy: PermissionPolicy{
			ReadRoots:  []string{dir},
			WriteRoots: []string{dir},
		},
	})
	if err := disabledRunner.EnsureBash("pwd"); err == nil || !strings.Contains(err.Error(), "disabled by policy") {
		t.Fatalf("expected disabled bash error, got %v", err)
	}

	allowedRunner := NewRunner(nil, NewRegistry(), RunnerConfig{
		WorkingDir: dir,
		PermissionPolicy: PermissionPolicy{
			ReadRoots:           []string{dir},
			WriteRoots:          []string{dir},
			BashEnabled:         true,
			BashAllowedPrefixes: []string{"go env", "go test"},
			BashBlockedPrefixes: []string{"rm ", "sudo "},
		},
	})
	if err := allowedRunner.EnsureBash(""); err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected missing command error, got %v", err)
	}
	if err := allowedRunner.EnsureBash("rm -rf /tmp/demo"); err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("expected blocked prefix error, got %v", err)
	}
	if err := allowedRunner.EnsureBash("ls -la"); err == nil || !strings.Contains(err.Error(), "allowed command prefix") {
		t.Fatalf("expected allowed prefix mismatch, got %v", err)
	}
	if err := allowedRunner.EnsureBash("go env GOROOT"); err != nil {
		t.Fatalf("expected allowed bash command to pass, got %v", err)
	}

	readOnlyRunner := NewRunner(nil, NewRegistry(), RunnerConfig{
		WorkingDir: dir,
		PermissionPolicy: PermissionPolicy{
			ReadRoots:   []string{dir},
			WriteRoots:  []string{dir},
			ReadOnly:    true,
			BashEnabled: true,
		},
	})
	if err := readOnlyRunner.EnsureBash("git commit -m test"); err == nil || !strings.Contains(err.Error(), "read-only mode") {
		t.Fatalf("expected read-only bash error, got %v", err)
	}
}

func TestPolicyHelperFunctions(t *testing.T) {
	if !bashCommandMayWrite("echo hi > file.txt") {
		t.Fatal("expected shell redirection to be treated as a write")
	}
	if !bashCommandMayWrite("git commit -m test") {
		t.Fatal("expected git commit to be treated as a write")
	}
	if !bashCommandMayWrite("sed -i s/a/b/g demo.txt") {
		t.Fatal("expected sed -i to be treated as a write")
	}
	if bashCommandMayWrite("go env GOROOT") {
		t.Fatal("expected read-only bash command to be treated as non-writing")
	}

	if !hasBashWriteRedirection("echo hi >> file.txt") {
		t.Fatal("expected append redirection to be detected")
	}
	if hasBashWriteRedirection("echo hi 2>&1") {
		t.Fatal("expected stderr redirection to be ignored")
	}

	root := t.TempDir()
	child := filepath.Join(root, "nested", "demo.txt")
	if !pathWithinAnyRoot(child, []string{root}) {
		t.Fatalf("expected child path %q to be within root %q", child, root)
	}
	if pathWithinAnyRoot(filepath.Join(t.TempDir(), "elsewhere"), []string{root}) {
		t.Fatal("expected unrelated path to be outside root")
	}

	if _, err := normalizePath(""); err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected empty path error, got %v", err)
	}
	if !matchesAnyPrefix("go env GOROOT", []string{"go env", "go test"}) {
		t.Fatal("expected prefix match to succeed")
	}
	if matchesAnyPrefix("ls -la", []string{"go env", "go test"}) {
		t.Fatal("expected unmatched prefix to fail")
	}

	var nilRunner *Runner
	if err := nilRunner.EnsureReadPath(filepath.Join(root, "demo.txt")); err != nil {
		t.Fatalf("expected nil runner read check to no-op, got %v", err)
	}
	if err := nilRunner.EnsureWritePath(filepath.Join(root, "demo.txt")); err != nil {
		t.Fatalf("expected nil runner write check to no-op, got %v", err)
	}
	if err := nilRunner.EnsureBash("pwd"); err != nil {
		t.Fatalf("expected nil runner bash check to no-op, got %v", err)
	}

	var nilExecCtx *ExecutionContext
	if err := nilExecCtx.EnsureReadPath(filepath.Join(root, "demo.txt")); err != nil {
		t.Fatalf("expected nil execution context read check to no-op, got %v", err)
	}
	if err := nilExecCtx.EnsureWritePath(filepath.Join(root, "demo.txt")); err != nil {
		t.Fatalf("expected nil execution context write check to no-op, got %v", err)
	}
	if err := nilExecCtx.EnsureBash("pwd"); err != nil {
		t.Fatalf("expected nil execution context bash check to no-op, got %v", err)
	}
}
