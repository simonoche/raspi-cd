package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
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
			&cli.StringFlag{
				Name:    "data-file",
				Aliases: []string{"f"},
				Usage:   "path to the JSON file used to persist tasks and agents across restarts",
				Value:   "/data/store.json",
				EnvVars: []string{"RASPICD_DATA_FILE"},
			},
			&cli.StringFlag{
				Name:    "signing-key",
				Aliases: []string{"sk"},
				Usage:   "Ed25519 private key seed as 64 hex chars (32 bytes). Generate once with: openssl rand -hex 32",
				EnvVars: []string{"RASPICD_SIGNING_KEY"},
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

	signingKey, err := loadOrGenerateSigningKey(c.String("signing-key"))
	if err != nil {
		return err
	}

	srv := server.New(c.String("bind"), c.String("secret"), c.String("agent-secret"), version, c.String("data-file"), c.Duration("agent-timeout"), static.FS, signingKey)

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

// loadOrGenerateSigningKey parses a hex-encoded 32-byte seed, or generates an
// ephemeral keypair and prints both values so the operator can make them permanent.
func loadOrGenerateSigningKey(seedHex string) (ed25519.PrivateKey, error) {
	if seedHex != "" {
		seed, err := hex.DecodeString(seedHex)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("RASPICD_SIGNING_KEY must be %d hex bytes (%d hex chars)", ed25519.SeedSize, ed25519.SeedSize*2)
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}

	// No key provided — generate an ephemeral one and tell the operator how to persist it.
	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}
	seedHex = hex.EncodeToString(privKey.Seed())
	pubKeyHex := hex.EncodeToString(privKey.Public().(ed25519.PublicKey))

	utils.Logger.Warn("RASPICD_SIGNING_KEY not set — using an ephemeral key that changes on every restart.")
	utils.Logger.Warnf("To make signing permanent, add to your server config:  RASPICD_SIGNING_KEY=%s", seedHex)
	utils.Logger.Warnf("Add to each agent's /etc/raspicd/agent.env:            RASPICD_VERIFY_KEY=%s", pubKeyHex)

	return privKey, nil
}
