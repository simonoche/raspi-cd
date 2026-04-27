package agent

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"regexp"

	"raspicd/internal/models"
	"raspicd/internal/utils"
)


// misc patterns
var segmentRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Executor runs tasks on the local machine.
type Executor struct {
	agentID    string
	scriptsDir string
	verifyKey  ed25519.PublicKey // nil means verification is disabled
}

// NewExecutor creates an Executor. verifyKey may be nil to disable signature verification.
func NewExecutor(agentID, scriptsDir string, verifyKey ed25519.PublicKey) *Executor {
	return &Executor{agentID: agentID, scriptsDir: scriptsDir, verifyKey: verifyKey}
}

// verifySignature checks the task's Ed25519 signature. If no verify key is
// configured it logs a warning and allows the task through.
func (e *Executor) verifySignature(task *models.Task) error {
	if len(e.verifyKey) == 0 {
		utils.Logger.Warn("RASPICD_VERIFY_KEY not set — skipping signature verification (task not authenticated)")
		return nil
	}
	if task.Signature == "" {
		return fmt.Errorf("task carries no signature")
	}
	sig, err := hex.DecodeString(task.Signature)
	if err != nil {
		return fmt.Errorf("malformed signature: %w", err)
	}
	msg, err := task.SigningMessage()
	if err != nil {
		return fmt.Errorf("build signing message: %w", err)
	}
	if !ed25519.Verify(e.verifyKey, msg, sig) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// Run executes a task and returns the result to be reported to the server.
func (e *Executor) Run(task *models.Task) models.TaskResultRequest {
	start := time.Now()
	result := models.TaskResultRequest{AgentID: e.agentID}

	if err := e.verifySignature(task); err != nil {
		result.Status = models.TaskStatusFailed
		result.Error = "signature verification failed: " + err.Error()
		utils.Logger.Errorf("Task %s rejected: %s", task.ID, result.Error)
		return result
	}

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
//
// Script resolution:
//   - Name must match [a-zA-Z0-9_-]+ segments separated by /
//   - Resolved to <scripts_dir>/<name>.sh
//   - Path must stay within scripts_dir (no traversal)
//
// Run-as user (optional):
//   - If <scripts_dir>/<name>.user exists, its content is used as the OS user
//   - Script is then run via: sudo -E -u <user> -- <script>
//
// Environment variables injected:
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

	if !isValidScriptName(name) {
		return "", fmt.Sprintf("invalid script name %q: use [a-zA-Z0-9_-] segments separated by /", name)
	}

	scriptPath := filepath.Join(e.scriptsDir, filepath.FromSlash(name)+".sh")

	// Defense in depth: ensure the resolved path stays within scriptsDir.
	cleanDir := filepath.Clean(e.scriptsDir) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(scriptPath)+string(filepath.Separator), cleanDir) {
		return "", "script path escapes scripts directory"
	}

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

	env := buildEnv(task.ID, e.agentID, task.Config)

	// Check for an optional companion .user file to run the script as a
	// specific OS user via sudo.
	userFilePath := filepath.Join(e.scriptsDir, filepath.FromSlash(name)+".user")
	runAs := readRunAsUser(userFilePath)

	var cmd *exec.Cmd
	if runAs != "" {
		utils.Logger.Infof("Running script %s as user %s (task %s)", scriptPath, runAs, task.ID)
		// -E preserves the environment (including RASPICD_* vars) for the script.
		cmd = exec.Command("sudo", "-E", "-u", runAs, "--", scriptPath)
	} else {
		utils.Logger.Infof("Running script %s (task %s)", scriptPath, task.ID)
		cmd = exec.Command(scriptPath)
	}
	cmd.Env = env

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Run(); err != nil {
		return buf.String(), err.Error()
	}
	return buf.String(), ""
}

// readRunAsUser reads the username from a .user companion file.
// Returns an empty string if the file does not exist or the username is invalid.
func readRunAsUser(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "" // file absent or unreadable — not an error
	}
	user := strings.TrimSpace(string(data))
	if !isValidSegment(user) {
		utils.Logger.Warnf("Ignoring invalid username %q in %s", user, path)
		return ""
	}
	return user
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

// isValidSegment returns true if matches regexp
func isValidSegment(s string) bool {
	return segmentRegex.MatchString(s)
}

// isValidScriptName returns true if name is one or more [a-zA-Z0-9_-] segments
// separated by forward slashes. Empty segments (leading/trailing slash,
// double slash) and dot segments are rejected.
func isValidScriptName(name string) bool {
	if name == "" {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if !isValidSegment(seg) {
			return false
		}
	}
	return true
}
