package agent

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"raspicd/internal/models"
)

// ---- isValidScriptName / isValidSegment ------------------------------------

func TestIsValidSegment(t *testing.T) {
	valid := []string{"deploy", "restart-service", "update_packages", "step1", "A1"}
	for _, s := range valid {
		if !isValidSegment(s) {
			t.Errorf("isValidSegment(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "has space", "dot.dot", "slash/ok", "../etc", "!!"}
	for _, s := range invalid {
		if isValidSegment(s) {
			t.Errorf("isValidSegment(%q) = true, want false", s)
		}
	}
}

func TestIsValidScriptName(t *testing.T) {
	valid := []string{
		"deploy",
		"scripts/deploy",
		"a/b/c",
		"my-script",
		"my_script",
	}
	for _, n := range valid {
		if !isValidScriptName(n) {
			t.Errorf("isValidScriptName(%q) = false, want true", n)
		}
	}

	invalid := []string{
		"",
		"/absolute",
		"trailing/",
		"double//slash",
		"../etc/passwd",
		"has space",
		"dot.segment",
	}
	for _, n := range invalid {
		if isValidScriptName(n) {
			t.Errorf("isValidScriptName(%q) = true, want false", n)
		}
	}
}

// ---- buildEnv ---------------------------------------------------------------

func TestBuildEnvBasic(t *testing.T) {
	env := buildEnv("task-1", "agent-1", nil)

	has := func(key string) bool {
		prefix := key + "="
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return true
			}
		}
		return false
	}
	get := func(key string) string {
		prefix := key + "="
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return strings.TrimPrefix(e, prefix)
			}
		}
		return ""
	}

	if !has("RASPICD_TASK_ID") {
		t.Error("RASPICD_TASK_ID not set")
	}
	if get("RASPICD_TASK_ID") != "task-1" {
		t.Errorf("RASPICD_TASK_ID: got %q, want task-1", get("RASPICD_TASK_ID"))
	}
	if get("RASPICD_AGENT_ID") != "agent-1" {
		t.Errorf("RASPICD_AGENT_ID: got %q, want agent-1", get("RASPICD_AGENT_ID"))
	}
	if has("RASPICD_CONFIG") {
		t.Error("RASPICD_CONFIG should not be set when config is nil")
	}
}

func TestBuildEnvWithConfig(t *testing.T) {
	config := map[string]interface{}{
		"env":     "prod",
		"replicas": 3.0,
		"debug":   true,
	}
	env := buildEnv("t1", "a1", config)

	get := func(key string) string {
		prefix := key + "="
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return strings.TrimPrefix(e, prefix)
			}
		}
		return ""
	}

	if get("RASPICD_CONFIG_ENV") != "prod" {
		t.Errorf("RASPICD_CONFIG_ENV: got %q, want prod", get("RASPICD_CONFIG_ENV"))
	}
	if get("RASPICD_CONFIG_REPLICAS") != "3" {
		t.Errorf("RASPICD_CONFIG_REPLICAS: got %q, want 3", get("RASPICD_CONFIG_REPLICAS"))
	}
	if get("RASPICD_CONFIG_DEBUG") != "true" {
		t.Errorf("RASPICD_CONFIG_DEBUG: got %q, want true", get("RASPICD_CONFIG_DEBUG"))
	}
	if get("RASPICD_CONFIG") == "" {
		t.Error("RASPICD_CONFIG should be set when config is non-empty")
	}
}

func TestBuildEnvNestedConfigOmitted(t *testing.T) {
	config := map[string]interface{}{
		"nested": map[string]interface{}{"key": "value"},
	}
	env := buildEnv("t1", "a1", config)
	for _, e := range env {
		if strings.HasPrefix(e, "RASPICD_CONFIG_NESTED=") {
			t.Error("nested objects should not produce individual RASPICD_CONFIG_* vars")
		}
	}
}

// ---- verifySignature --------------------------------------------------------

func makeSignedTask(t *testing.T, privKey ed25519.PrivateKey) *models.Task {
	t.Helper()
	task := &models.Task{ID: "t1", Script: "deploy", Config: map[string]interface{}{"env": "prod"}}
	msg, err := task.SigningMessage()
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(privKey, msg)
	task.Signature = hex.EncodeToString(sig)
	return task
}

func TestVerifySignatureValid(t *testing.T) {
	pubKey, privKey, _ := ed25519.GenerateKey(nil)
	task := makeSignedTask(t, privKey)
	e := NewExecutor("pi1", "/tmp", pubKey)
	if err := e.verifySignature(task); err != nil {
		t.Errorf("expected valid signature, got: %v", err)
	}
}

func TestVerifySignatureInvalid(t *testing.T) {
	pubKey, privKey, _ := ed25519.GenerateKey(nil)
	task := makeSignedTask(t, privKey)
	task.Script = "tampered" // invalidate signature
	e := NewExecutor("pi1", "/tmp", pubKey)
	if err := e.verifySignature(task); err == nil {
		t.Error("expected signature mismatch error, got nil")
	}
}

func TestVerifySignatureMissing(t *testing.T) {
	pubKey, _, _ := ed25519.GenerateKey(nil)
	task := &models.Task{ID: "t1", Script: "deploy"}
	e := NewExecutor("pi1", "/tmp", pubKey)
	if err := e.verifySignature(task); err == nil {
		t.Error("expected error for missing signature")
	}
}

func TestVerifySignatureMalformed(t *testing.T) {
	pubKey, _, _ := ed25519.GenerateKey(nil)
	task := &models.Task{ID: "t1", Script: "deploy", Signature: "not-hex!!"}
	e := NewExecutor("pi1", "/tmp", pubKey)
	if err := e.verifySignature(task); err == nil {
		t.Error("expected error for malformed signature")
	}
}

func TestVerifySignatureNoKey(t *testing.T) {
	// nil verify key disables verification (log warning, allow through)
	e := NewExecutor("pi1", "/tmp", nil)
	task := &models.Task{ID: "t1", Script: "deploy"}
	if err := e.verifySignature(task); err != nil {
		t.Errorf("expected nil when no verify key, got: %v", err)
	}
}

func TestVerifySignatureWrongKey(t *testing.T) {
	_, privKey, _ := ed25519.GenerateKey(nil)
	otherPub, _, _ := ed25519.GenerateKey(nil)
	task := makeSignedTask(t, privKey)
	e := NewExecutor("pi1", "/tmp", otherPub)
	if err := e.verifySignature(task); err == nil {
		t.Error("expected signature mismatch with wrong public key")
	}
}

// ---- Executor.Run -----------------------------------------------------------

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newUnsignedExecutor(t *testing.T, scriptsDir string) *Executor {
	t.Helper()
	return NewExecutor("pi1", scriptsDir, nil) // nil = no verify key
}

func TestRunSuccess(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "greet.sh", "#!/bin/sh\necho hello\n")
	e := newUnsignedExecutor(t, dir)
	task := &models.Task{ID: "t1", Script: "greet"}
	result := e.Run(task)
	if result.Status != models.TaskStatusCompleted {
		t.Errorf("status: got %q, want completed (error: %s)", result.Status, result.Error)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Errorf("output: got %q, want to contain hello", result.Output)
	}
}

func TestRunScriptNotFound(t *testing.T) {
	dir := t.TempDir()
	e := newUnsignedExecutor(t, dir)
	task := &models.Task{ID: "t1", Script: "noexist"}
	result := e.Run(task)
	if result.Status != models.TaskStatusFailed {
		t.Errorf("status: got %q, want failed", result.Status)
	}
	if result.Error == "" {
		t.Error("expected non-empty error for missing script")
	}
}

func TestRunScriptNotExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nox.sh")
	os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0644) // not executable
	e := newUnsignedExecutor(t, dir)
	task := &models.Task{ID: "t1", Script: "nox"}
	result := e.Run(task)
	if result.Status != models.TaskStatusFailed {
		t.Errorf("status: got %q, want failed", result.Status)
	}
}

func TestRunInvalidScriptName(t *testing.T) {
	dir := t.TempDir()
	e := newUnsignedExecutor(t, dir)
	task := &models.Task{ID: "t1", Script: "../etc/passwd"}
	result := e.Run(task)
	if result.Status != models.TaskStatusFailed {
		t.Errorf("status: got %q, want failed", result.Status)
	}
}

func TestRunScriptExitCode(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "fail.sh", "#!/bin/sh\nexit 1\n")
	e := newUnsignedExecutor(t, dir)
	task := &models.Task{ID: "t1", Script: "fail"}
	result := e.Run(task)
	if result.Status != models.TaskStatusFailed {
		t.Errorf("status: got %q, want failed", result.Status)
	}
	if result.Error == "" {
		t.Error("expected non-empty error for non-zero exit")
	}
}

func TestRunInjectsEnvVars(t *testing.T) {
	dir := t.TempDir()
	// Script prints key env vars to stdout.
	writeScript(t, dir, "env-check.sh", "#!/bin/sh\necho TASK=$RASPICD_TASK_ID AGENT=$RASPICD_AGENT_ID ENV=$RASPICD_CONFIG_ENV\n")
	e := NewExecutor("my-agent", dir, nil)
	task := &models.Task{
		ID:     "task-abc",
		Script: "env-check",
		Config: map[string]interface{}{"env": "staging"},
	}
	result := e.Run(task)
	if result.Status != models.TaskStatusCompleted {
		t.Fatalf("status: got %q (error: %s)", result.Status, result.Error)
	}
	if !strings.Contains(result.Output, "TASK=task-abc") {
		t.Errorf("output missing TASK: %q", result.Output)
	}
	if !strings.Contains(result.Output, "AGENT=my-agent") {
		t.Errorf("output missing AGENT: %q", result.Output)
	}
	if !strings.Contains(result.Output, "ENV=staging") {
		t.Errorf("output missing ENV: %q", result.Output)
	}
}

func TestRunSubdirScript(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "sub/deploy.sh", "#!/bin/sh\necho subdir-ok\n")
	e := newUnsignedExecutor(t, dir)
	task := &models.Task{ID: "t1", Script: "sub/deploy"}
	result := e.Run(task)
	if result.Status != models.TaskStatusCompleted {
		t.Errorf("status: got %q (error: %s)", result.Status, result.Error)
	}
	if !strings.Contains(result.Output, "subdir-ok") {
		t.Errorf("output: got %q", result.Output)
	}
}

func TestRunSignatureVerifiedBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "deploy.sh", "#!/bin/sh\necho should-not-run\n")

	pubKey, _, _ := ed25519.GenerateKey(nil)
	e := NewExecutor("pi1", dir, pubKey)
	// Task has no signature but executor requires one.
	task := &models.Task{ID: "t1", Script: "deploy"}
	result := e.Run(task)
	if result.Status != models.TaskStatusFailed {
		t.Errorf("status: got %q, want failed", result.Status)
	}
	if result.Output == "should-not-run" {
		t.Error("script must not execute when signature verification fails")
	}
}

func TestRunDurationSet(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "quick.sh", "#!/bin/sh\nexit 0\n")
	e := newUnsignedExecutor(t, dir)
	task := &models.Task{ID: "t1", Script: "quick"}
	result := e.Run(task)
	if result.DurationMs < 0 {
		t.Errorf("DurationMs should be non-negative, got %d", result.DurationMs)
	}
}
