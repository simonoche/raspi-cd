package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"

	"raspicd/internal/server"
	"raspicd/internal/utils"
	"raspicd/static"
)

var version = "dev"

func main() {
	app := &cli.App{
		Name:    "raspicd-server",
		Usage:   "Central control server for RasPiCD",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "bind",
				Aliases: []string{"b"},
				Usage:   "listen address",
				Value:   ":8080",
				EnvVars: []string{"RASPICD_BIND"},
			},
			&cli.StringFlag{
				Name:     "secret",
				Aliases:  []string{"k"},
				Usage:    "CI/CD Bearer token secret (used by pipelines to create tasks)",
				EnvVars:  []string{"RASPICD_SECRET"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "agent-secret",
				Aliases:  []string{"K"},
				Usage:    "agent Bearer token secret (used by agents to poll and report)",
				EnvVars:  []string{"RASPICD_AGENT_SECRET"},
				Required: true,
			},
			&cli.DurationFlag{
				Name:    "agent-timeout",
				Aliases: []string{"t"},
				Usage:   "mark agents offline after this duration without a heartbeat",
				Value:   90 * time.Second,
				EnvVars: []string{"RASPICD_AGENT_TIMEOUT"},
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"D"},
				Usage:   "verbose logging",
				EnvVars: []string{"RASPICD_DEBUG"},
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
	utils.Logger.Infof("RasPiCD Server %s", version)

	srv := server.New(c.String("bind"), c.String("secret"), c.String("agent-secret"), version, c.Duration("agent-timeout"), static.FS)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			utils.Logger.Errorf("Server error: %v", err)
		}
	}()

	<-sigCh
	return srv.Stop()
}
