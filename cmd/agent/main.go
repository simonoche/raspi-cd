package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"

	"raspicd/internal/agent"
	"raspicd/internal/utils"
)

var version = "dev"

func main() {
	app := &cli.App{
		Name:    "raspicd-agent",
		Usage:   "RasPiCD agent daemon for Raspberry Pi",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "server",
				Aliases:  []string{"s"},
				Usage:    "server base URL (http:// or https://)",
				EnvVars:  []string{"RASPICD_SERVER"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "agent-id",
				Aliases:  []string{"id"},
				Usage:    "unique agent name",
				EnvVars:  []string{"RASPICD_AGENT_ID"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "secret",
				Aliases:  []string{"k"},
				Usage:    "agent Bearer token secret",
				EnvVars:  []string{"RASPICD_AGENT_SECRET"},
				Required: true,
			},
			&cli.StringFlag{
				Name:    "hostname",
				Aliases: []string{"n"},
				Usage:   "display name for this agent",
				EnvVars: []string{"HOSTNAME"},
				Value:   "raspicd-agent",
			},
			&cli.DurationFlag{
				Name:    "interval",
				Aliases: []string{"i"},
				Usage:   "maximum retry delay after a failed connection (exponential backoff from 1s up to this value)",
				Value:   60 * time.Second,
				EnvVars: []string{"RASPICD_POLL_INTERVAL"},
			},
			&cli.StringFlag{
				Name:    "scripts-dir",
				Aliases: []string{"S"},
				Usage:   "directory containing named scripts (.sh files)",
				Value:   "/etc/raspicd/scripts",
				EnvVars: []string{"RASPICD_SCRIPTS_DIR"},
			},
			&cli.StringFlag{
				Name:    "verify-key",
				Aliases: []string{"vk"},
				Usage:   "Ed25519 public key as 64 hex chars — verify task signatures from the server",
				EnvVars: []string{"RASPICD_VERIFY_KEY"},
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
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

	agentID := c.String("agent-id")
	hostname := c.String("hostname")
	maxRetry := c.Duration("interval")
	scriptsDir := c.String("scripts-dir")
	serverURL := c.String("server")

	utils.Logger.Infof("RasPiCD Agent %s  id=%s  server=%s  max-retry=%s  scripts=%s",
		version, agentID, serverURL, maxRetry, scriptsDir)

	var verifyKey ed25519.PublicKey
	if vkHex := c.String("verify-key"); vkHex != "" {
		b, err := hex.DecodeString(vkHex)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return fmt.Errorf("RASPICD_VERIFY_KEY must be %d hex bytes (%d hex chars)", ed25519.PublicKeySize, ed25519.PublicKeySize*2)
		}
		verifyKey = ed25519.PublicKey(b)
		utils.Logger.Infof("Task signature verification enabled")
	} else {
		utils.Logger.Warn("RASPICD_VERIFY_KEY not set — task signatures will not be verified")
	}

	client := agent.NewClient(serverURL, agentID, c.String("secret"))
	executor := agent.NewExecutor(agentID, scriptsDir, verifyKey)

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Verify server's public key matches our expected key (if configured)
	if vkHex := c.String("verify-key"); vkHex != "" {
		utils.Logger.Info("Verifying server public key during handshake...")
		if err := client.VerifyServerPublicKey(ctx, vkHex); err != nil {
			utils.Logger.Errorf("Public key verification failed: %v", err)
			return err
		}
	}

	bo := newBackoff(maxRetry)
	fails := 0

	utils.Logger.Infof("Connecting to %s ...", serverURL)

	for ctx.Err() == nil {
		err := client.Connect(ctx, hostname, version, executor)
		if ctx.Err() != nil {
			break // clean shutdown
		}
		fails++

		// Check for fatal errors that should not be retried
		errMsg := err.Error()
		if strings.Contains(errMsg, "server rejected connection") {
			utils.Logger.Errorf("Fatal error: %v (not retrying)", err)
			return err
		}

		delay := bo.next()
		utils.Logger.Warnf("Connection lost (attempt %d): %v. Retrying in %s ...",
			fails, err, delay.Round(time.Millisecond))
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
	}

	utils.Logger.Info("Agent shutting down")
	return nil
}

// backoff implements exponential backoff with ±25% jitter.
type backoff struct {
	current time.Duration
	max     time.Duration
	rng     *rand.Rand
}

func newBackoff(max time.Duration) *backoff {
	if max < time.Second {
		max = time.Second
	}
	return &backoff{
		max: max,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (b *backoff) next() time.Duration {
	const base = time.Second
	if b.current < base {
		b.current = base
	} else {
		b.current *= 2
		if b.current > b.max {
			b.current = b.max
		}
	}
	quarter := int64(b.current / 4)
	jitter := time.Duration(b.rng.Int63n(quarter*2+1) - quarter)
	return b.current + jitter
}

func (b *backoff) reset() {
	b.current = 0
}
