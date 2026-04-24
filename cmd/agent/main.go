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

	utils.Logger.Infof("RaspiDeploy agent v%s  id=%s  server=%s  interval=%s",
		version, agentID, c.String("server"), interval)

	client := agent.NewClient(c.String("server"), agentID, c.String("secret"))
	executor := agent.NewExecutor(agentID)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			utils.Logger.Info("shutting down")
			return nil

		case <-ticker.C:
			if err := client.SendHeartbeat(hostname, version); err != nil {
				utils.Logger.Warnf("heartbeat failed: %v", err)
				continue
			}

			tasks, err := client.FetchTasks()
			if err != nil {
				utils.Logger.Warnf("fetch tasks failed: %v", err)
				continue
			}

			for _, task := range tasks {
				// Tell the server we picked up the task before executing it.
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
	}
}
