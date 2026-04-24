package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"

	"raspideploy/internal/agent"
	"raspideploy/internal/models"
	"raspideploy/internal/utils"
)

var version = "0.1.0"

func main() {
	app := &cli.App{
		Name:    "raspideploy-agent",
		Usage:   "RaspiDeploy agent daemon for Raspberry Pi",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "server",
				Aliases:  []string{"s"},
				Usage:    "server base URL",
				EnvVars:  []string{"RASPIDEPLOY_SERVER"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "agent-id",
				Aliases:  []string{"id"},
				Usage:    "unique agent name",
				EnvVars:  []string{"RASPIDEPLOY_AGENT_ID"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "secret",
				Aliases:  []string{"k"},
				Usage:    "shared Bearer token secret",
				EnvVars:  []string{"RASPIDEPLOY_SECRET"},
				Required: true,
			},
			&cli.StringFlag{
				Name:    "hostname",
				Aliases: []string{"n"},
				Usage:   "display name for this agent",
				EnvVars: []string{"HOSTNAME"},
				Value:   "raspideploy-agent",
			},
			&cli.DurationFlag{
				Name:    "interval",
				Aliases: []string{"i"},
				Usage:   "polling interval",
				Value:   30 * time.Second,
				EnvVars: []string{"RASPIDEPLOY_POLL_INTERVAL"},
			},
			&cli.StringFlag{
				Name:    "scripts-dir",
				Aliases: []string{"S"},
				Usage:   "directory containing named scripts (.sh files)",
				Value:   "/etc/raspideploy/scripts",
				EnvVars: []string{"RASPIDEPLOY_SCRIPTS_DIR"},
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "verbose logging",
				EnvVars: []string{"RASPIDEPLOY_DEBUG"},
			},
		},
		Action: run,
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(c *cli.Context) error {
	if c.Bool("debug") {
		utils.SetDebugLevel()
	}

	agentID := c.String("agent-id")
	hostname := c.String("hostname")
	interval := c.Duration("interval")
	scriptsDir := c.String("scripts-dir")

	utils.Logger.Infof("RaspiDeploy agent v%s  id=%s  server=%s  interval=%s  scripts=%s",
		version, agentID, c.String("server"), interval, scriptsDir)

	client := agent.NewClient(c.String("server"), agentID, c.String("secret"))
	executor := agent.NewExecutor(agentID, scriptsDir)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Contact the server immediately on startup instead of waiting for the
	// first tick — the server would otherwise not see this agent as "online"
	// until the full interval has elapsed.
	utils.Logger.Infof("connecting to server on startup ...")
	poll(client, executor, agentID, hostname)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			utils.Logger.Info("agent shutting down")
			return nil
		case <-ticker.C:
			poll(client, executor, agentID, hostname)
		}
	}
}

// poll sends a heartbeat, fetches pending tasks, and executes them.
func poll(client *agent.Client, executor *agent.Executor, agentID, hostname string) {
	if err := client.SendHeartbeat(hostname, version); err != nil {
		utils.Logger.Warnf("heartbeat failed: %v", err)
		return
	}

	tasks, err := client.FetchTasks()
	if err != nil {
		utils.Logger.Warnf("fetch tasks failed: %v", err)
		return
	}

	for _, task := range tasks {
		utils.Logger.Infof("picked up task %s (type: %s)", task.ID, task.Type)

		// Tell the server we have started before executing — if we crash
		// mid-task the server will show "running" rather than "pending".
		_ = client.ReportResult(task.ID, models.TaskResultRequest{
			AgentID: agentID,
			Status:  models.TaskStatusRunning,
		})

		result := executor.Run(task)

		if err := client.ReportResult(task.ID, result); err != nil {
			utils.Logger.Errorf("report result failed for task %s: %v", task.ID, err)
		}
	}
}
