package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"raspideploy/internal/models"
	"raspideploy/internal/utils"
)

// Executor runs tasks on the local machine.
type Executor struct {
	agentID    string
	scriptsDir string
}

// NewExecutor creates an Executor.
func NewExecutor(agentID, scriptsDir string) *Executor {
	return &Executor{agentID: agentID, scriptsDir: scriptsDir}
}

// Run executes a task and returns the result to be reported to the server.
func (e *Executor) Run(task *models.Task) models.TaskResultRequest {
	start := time.Now()
	result := models.TaskResultRequest{AgentID: e.agentID}

	utils.Logger.Infof("executing task %s (type: %s)", task.ID, task.Type)

	switch task.Type {
	case models.TaskTypeDeploy:
		result.Output, result.Error = e.deploy(task.Payload)
	case models.TaskTypeScript:
		result.Output, result.Error = e.script(task.Payload)
	case models.TaskTypeRestart:
		result.Output, result.Error = e.restart(task.Payload)
	case models.TaskTypeNamedScript:
		result.Output, result.Error = e.namedScript(task)
	default:
		result.Error = fmt.Sprintf("unknown task type: %s", task.Type)
	}

	result.DurationMs = time.Since(start).Milliseconds()
	if result.Error == "" {
		result.Status = models.TaskStatusCompleted
		utils.Logger.Infof("task %s completed in %dms", task.ID, result.DurationMs)
	} else {
		result.Status = models.TaskStatusFailed
		utils.Logger.Errorf("task %s failed: %s", task.ID, result.Error)
	}
	return result
}

// namedScript looks up a pre-installed script by name and runs it,
// exposing the task's config payload as environment variables.
//
// Payload:
//
//	{
//	  "name":   "deploy-myapp",         // required — maps to <scripts_dir>/deploy-myapp.sh
//	  "config": { "key": "value", ... } // optional — passed as env vars to the script
//	}
//
// Environment variables injected into the script:
//
//	RASPIDEPLOY_TASK_ID          — ID of this task
//	RASPIDEPLOY_AGENT_ID         — ID of this agent
//	RASPIDEPLOY_CONFIG           — full config as a JSON string
//	RASPIDEPLOY_CONFIG_<KEY>     — one var per top-level config key (string/number/bool only)
func (e *Executor) namedScript(task *models.Task) (output, errMsg string) {
	name, _ := task.Payload["name"].(string)
	if name == "" {
		return "", "missing name in payload"
	}

	// Prevent path traversal: only [a-zA-Z0-9_-] are allowed in script names.
	if !isValidScriptName(name) {
		return "", fmt.Sprintf("invalid script name %q: only letters, digits, - and _ are allowed", name)
	}

	scriptPath := filepath.Join(e.scriptsDir, name+".sh")

	info, err := os.Stat(scriptPath)
	if os.IsNotExist(err) {
		return "", fmt.Sprintf("script %q not found (looked in %s)", name, e.scriptsDir)
	}
	if err != nil {
		return "", fmt.Sprintf("cannot stat script: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		return "", fmt.Sprintf("script %s is not executable (run: chmod +x %s)", scriptPath, scriptPath)
	}

	config, _ := task.Payload["config"].(map[string]interface{})

	cmd := exec.Command(scriptPath)
	cmd.Env = buildEnv(task.ID, e.agentID, config)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	utils.Logger.Infof("running script %s (task %s)", scriptPath, task.ID)

	if err := cmd.Run(); err != nil {
		return buf.String(), err.Error()
	}
	return buf.String(), ""
}

// buildEnv constructs the environment for a named script.
// It starts from the current process environment so the script inherits PATH,
// HOME, etc., then appends RASPIDEPLOY_* variables.
func buildEnv(taskID, agentID string, config map[string]interface{}) []string {
	env := append(os.Environ(),
		"RASPIDEPLOY_TASK_ID="+taskID,
		"RASPIDEPLOY_AGENT_ID="+agentID,
	)

	if len(config) == 0 {
		return env
	}

	// Full JSON blob — useful for complex/nested config.
	if raw, err := json.Marshal(config); err == nil {
		env = append(env, "RASPIDEPLOY_CONFIG="+string(raw))
	}

	// Individual vars for top-level scalar values — no jq required in simple scripts.
	for k, v := range config {
		key := "RASPIDEPLOY_CONFIG_" + strings.ToUpper(k)
		switch val := v.(type) {
		case string:
			env = append(env, key+"="+val)
		case float64:
			env = append(env, key+"="+strconv.FormatFloat(val, 'f', -1, 64))
		case bool:
			env = append(env, key+"="+strconv.FormatBool(val))
		// nested objects/arrays are only available via RASPIDEPLOY_CONFIG
		}
	}

	return env
}

// isValidScriptName returns true if name contains only [a-zA-Z0-9_-].
func isValidScriptName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// ---- existing task types ---------------------------------------------------

// deploy clones or updates a git repository, then runs the provided commands.
//
// Required payload fields:
//   - repo_url   — git remote URL
//   - target_dir — local path on the Pi
//
// Optional payload fields:
//   - ref      — branch or tag to checkout (default: "main")
//   - commands — []string of shell commands executed inside target_dir
func (e *Executor) deploy(payload map[string]interface{}) (output, errMsg string) {
	repoURL, _ := payload["repo_url"].(string)
	targetDir, _ := payload["target_dir"].(string)
	ref, _ := payload["ref"].(string)

	if repoURL == "" {
		return "", "missing repo_url in payload"
	}
	if targetDir == "" {
		return "", "missing target_dir in payload"
	}
	if ref == "" {
		ref = "main"
	}

	utils.Logger.Infof("deploy: repo=%s ref=%s dir=%s", repoURL, ref, targetDir)

	var buf bytes.Buffer

	_, statErr := os.Stat(targetDir + "/.git")
	switch {
	case os.IsNotExist(statErr):
		utils.Logger.Infof("deploy: cloning %s (branch/tag: %s)", repoURL, ref)
		if err := runCmd(&buf, "", "git", "clone", "--branch", ref, repoURL, targetDir); err != nil {
			return buf.String(), err.Error()
		}
	case statErr == nil:
		utils.Logger.Infof("deploy: updating existing repo at %s to %s", targetDir, ref)
		if err := runCmd(&buf, "", "git", "-C", targetDir, "fetch", "--tags", "--prune", "origin"); err != nil {
			return buf.String(), err.Error()
		}
		if err := runCmd(&buf, "", "git", "-C", targetDir, "checkout", "-f", ref); err != nil {
			return buf.String(), err.Error()
		}
		// best-effort: succeeds for branches, silently fails for tags (detached HEAD)
		_ = runCmd(&buf, "", "git", "-C", targetDir, "pull", "--ff-only")
	default:
		return "", fmt.Sprintf("stat %s: %v", targetDir+"/.git", statErr)
	}

	rawCmds, _ := payload["commands"].([]interface{})
	for _, raw := range rawCmds {
		line, ok := raw.(string)
		if !ok {
			continue
		}
		utils.Logger.Infof("deploy: running command: %s", line)
		fmt.Fprintf(&buf, "$ %s\n", line)
		if err := runCmd(&buf, targetDir, "bash", "-c", line); err != nil {
			return buf.String(), err.Error()
		}
	}

	return buf.String(), ""
}

// script runs an arbitrary shell script.
// Payload: { "script": "<bash commands>" }
func (e *Executor) script(payload map[string]interface{}) (output, errMsg string) {
	script, _ := payload["script"].(string)
	if script == "" {
		return "", "missing script in payload"
	}
	utils.Logger.Infof("script: running inline script (%d bytes)", len(script))
	var buf bytes.Buffer
	if err := runCmd(&buf, "", "bash", "-c", script); err != nil {
		return buf.String(), err.Error()
	}
	return buf.String(), ""
}

// restart restarts a systemd service.
// Payload: { "service": "<name>" }
func (e *Executor) restart(payload map[string]interface{}) (output, errMsg string) {
	service, _ := payload["service"].(string)
	if service == "" {
		return "", "missing service in payload"
	}
	utils.Logger.Infof("restart: service=%s", service)
	var buf bytes.Buffer
	if err := runCmd(&buf, "", "systemctl", "restart", service); err != nil {
		return buf.String(), err.Error()
	}
	return buf.String(), ""
}

// runCmd executes a command, writing combined stdout+stderr to buf.
func runCmd(buf *bytes.Buffer, dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = buf
	cmd.Stderr = buf
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Run()
}
