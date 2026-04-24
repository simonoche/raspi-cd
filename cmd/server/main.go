package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"

	"raspideploy/internal/server"
	"raspideploy/internal/utils"
)

var version = "0.1.0"

func main() {
	app := &cli.App{
		Name:    "raspideploy-server",
		Usage:   "Central control server for RaspiDeploy",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "bind",
				Aliases: []string{"b"},
				Usage:   "listen address",
				Value:   ":8080",
				EnvVars: []string{"RASPIDEPLOY_BIND"},
			},
			&cli.StringFlag{
				Name:     "secret",
				Aliases:  []string{"k"},
				Usage:    "CI/CD Bearer token secret (used by pipelines to create tasks)",
				EnvVars:  []string{"RASPIDEPLOY_SECRET"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "agent-secret",
				Aliases:  []string{"K"},
				Usage:    "agent Bearer token secret (used by agents to poll and report)",
				EnvVars:  []string{"RASPIDEPLOY_AGENT_SECRET"},
				Required: true,
			},
			&cli.DurationFlag{
				Name:    "agent-timeout",
				Aliases: []string{"t"},
				Usage:   "mark agents offline after this duration without a heartbeat",
				Value:   90 * time.Second,
				EnvVars: []string{"RASPIDEPLOY_AGENT_TIMEOUT"},
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"D"},
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
	utils.Logger.Infof("RaspiDeploy server v%s", version)

	srv := server.New(c.String("bind"), c.String("secret"), c.String("agent-secret"), c.Duration("agent-timeout"))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			utils.Logger.Errorf("server error: %v", err)
		}
	}()

	<-sigCh
	return srv.Stop()
}
