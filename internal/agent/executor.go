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

	"raspicd/internal/models"
	"raspicd/internal/utils"
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

	utils.Logger.Infof("Executing task %s (script: %s)", task.ID, task.Script)

	result.Output, result.Error = e.run(task)

	result.DurationMs = time.Since(start).Milliseconds()
	if result.Error == "" {
		result.Status = models.TaskStatusCompleted
		utils.Logger.Infof("Task %s completed in %dms", task.ID, result.DurationMs)
	} else {
		result.Status = models.TaskStatusFailed
		utils.Logger.Errorf("Task %s failed: %s", task.ID, result.Error)
	}
	return result
}

// run looks up a pre-installed script by name and executes it.
// The script must exist at <scripts_dir>/<name>.sh and be executable.
// task.Config is passed as environment variables:
//
//	RASPICD_TASK_ID          — ID of this task
//	RASPICD_AGENT_ID         — ID of this agent
//	RASPICD_CONFIG           — full config as a JSON string
//	RASPICD_CONFIG_<KEY>     — one var per top-level config key (string/number/bool only)
func (e *Executor) run(task *models.Task) (output, errMsg string) {
	name := task.Script
	if name == "" {
		return "", "missing script name"
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

	cmd := exec.Command(scriptPath)
	cmd.Env = buildEnv(task.ID, e.agentID, task.Config)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	utils.Logger.Infof("Running script %s (task %s)", scriptPath, task.ID)

	if err := cmd.Run(); err != nil {
		return buf.String(), err.Error()
	}
	return buf.String(), ""
}

// buildEnv constructs the environment for a script.
// It starts from the current process environment so the script inherits PATH,
// HOME, etc., then appends RASPICD_* variables.
func buildEnv(taskID, agentID string, config map[string]interface{}) []string {
	env := append(os.Environ(),
		"RASPICD_TASK_ID="+taskID,
		"RASPICD_AGENT_ID="+agentID,
	)

	if len(config) == 0 {
		return env
	}

	// Full JSON blob — useful for complex/nested config.
	if raw, err := json.Marshal(config); err == nil {
		env = append(env, "RASPICD_CONFIG="+string(raw))
	}

	// Individual vars for top-level scalar values — no jq required in simple scripts.
	for k, v := range config {
		key := "RASPICD_CONFIG_" + strings.ToUpper(k)
		switch val := v.(type) {
		case string:
			env = append(env, key+"="+val)
		case float64:
			env = append(env, key+"="+strconv.FormatFloat(val, 'f', -1, 64))
		case bool:
			env = append(env, key+"="+strconv.FormatBool(val))
			// nested objects/arrays are only available via RASPICD_CONFIG
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
