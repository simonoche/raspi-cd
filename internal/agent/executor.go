package agent

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"time"

	"raspideploy/internal/models"
	"raspideploy/internal/utils"
)

// Executor runs tasks on the local machine.
type Executor struct {
	agentID string
}

// NewExecutor creates an Executor.
func NewExecutor(agentID string) *Executor {
	return &Executor{agentID: agentID}
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

	var buf bytes.Buffer

	_, statErr := os.Stat(targetDir + "/.git")
	switch {
	case os.IsNotExist(statErr):
		if err := runCmd(&buf, "", "git", "clone", "--branch", ref, repoURL, targetDir); err != nil {
			return buf.String(), err.Error()
		}
	case statErr == nil:
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
