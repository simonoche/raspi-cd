# RaspiDeploy — Implementation Reference

## Overview

RaspiDeploy is a lightweight remote-deployment system for Raspberry Pis that sit behind a NAT (no public IP, no exposed ports). It consists of two binaries:

- **Server** — a publicly accessible REST API. CI/CD pipelines push deployment tasks to it. Runs in Docker.
- **Agent** — a daemon installed on each Raspberry Pi. It polls the server, picks up tasks, and executes them locally. No inbound connectivity required.

---

## Architecture

```
┌─────────────────────┐         HTTPS          ┌─────────────────────┐
│   CI/CD pipeline    │ ──── POST /tasks ────▶  │                     │
│ (GitHub Actions /   │                         │       Server        │
│  GitLab CI / curl)  │                         │   (public internet) │
└─────────────────────┘                         │                     │
                                                └──────────┬──────────┘
                                                           │
                                      ┌────────────────────┼──────────────────────┐
                                      │  Poll /tasks       │  POST /tasks/result  │
                              ┌───────▼────────┐   ┌───────▼────────┐
                              │   Agent (Pi 1) │   │   Agent (Pi 2) │
                              │  192.168.1.10  │   │  192.168.1.11  │
                              └────────────────┘   └────────────────┘
```

**Key design constraint:** agents initiate all connections outbound. The server never connects to agents. This means agents behind NAT/firewalls work out-of-the-box.

---

## Security Model

### Authentication

- A single shared secret (`RASPIDEPLOY_SECRET`) is required on both sides.
- Every API request (including agent calls) must carry the header `Authorization: Bearer <secret>`.
- The `/health` endpoint is unauthenticated (used for Docker health checks and uptime monitoring).
- `crypto/subtle.ConstantTimeCompare` is used for token comparison to prevent timing attacks.
- Unauthorized requests are logged at WARN level with the remote IP.

### Named script execution (Option A)

The recommended task type for secure execution is `named_script`. Instead of sending raw shell commands over the wire, CI/CD sends only a **script name**. The actual script lives on the Pi at a pre-configured directory.

Security checks the agent performs before executing any named script:

1. **Name validation** — script name must match `[a-zA-Z0-9_-]+`. Slashes, dots, and shell metacharacters are rejected, preventing path traversal (`../etc/passwd`) and injection attacks.
2. **File existence** — the script must exist under `scripts_dir`. If not found, the task fails with a clear error.
3. **Executability** — the file must have the execute bit set (`chmod +x`). This prevents accidentally running untrusted files copied to the directory.

---

## Project Structure

```
raspicd/
├── cmd/
│   ├── server/
│   │   └── main.go                  # Server entrypoint (CLI flags, signal handling)
│   └── agent/
│       └── main.go                  # Agent entrypoint (CLI flags, poll loop)
├── internal/
│   ├── server/
│   │   ├── server.go                # HTTP server, route setup, auth middleware, stale sweep
│   │   ├── handlers.go              # One handler per route
│   │   └── store.go                 # In-memory store behind an interface
│   ├── agent/
│   │   ├── client.go                # Auth-aware HTTP client
│   │   └── executor.go              # Task execution (deploy, script, restart, named_script)
│   ├── models/
│   │   └── models.go                # Shared types: Task, Agent, TaskStatus, payloads
│   └── utils/
│       └── logger.go                # Global logrus logger
├── .github/
│   └── workflows/
│       ├── ci.yml                   # Test on push / PR
│       └── release.yml              # Build + publish on vX.Y.Z tag
├── examples/
│   ├── github-actions/              # Ready-to-use GitHub Actions workflows
│   └── named-scripts/               # Example named scripts for the Pi
├── deploy/
│   ├── agent.service                # systemd unit file
│   └── agent.env.example            # Template for /etc/raspicd/agent.env
├── Dockerfile.server                # Multi-stage build, TARGETARCH for multi-arch
├── docker-compose.yml               # Server + named volume
├── Makefile                         # build, build-arm*, test, docker-* targets
├── go.mod
└── .gitignore
```

---

## Server

### CLI flags / environment variables

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--bind` / `-b` | `RASPIDEPLOY_BIND` | `:8080` | Listen address |
| `--secret` / `-k` | `RASPIDEPLOY_SECRET` | — | **Required.** Shared auth secret |
| `--agent-timeout` / `-t` | `RASPIDEPLOY_AGENT_TIMEOUT` | `90s` | Mark agents offline after this duration without a heartbeat |
| `--debug` / `-D` | `RASPIDEPLOY_DEBUG` | `false` | Verbose logging |

### REST API

All routes except `/health` require `Authorization: Bearer <secret>`.

#### Health

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/health` | No | Returns `{"status":"healthy"}` — silent in logs (polled by Docker/load balancers) |

#### Agents

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/api/v1/agents` | Yes | List all registered agents and their status |
| `POST` | `/api/v1/agents/heartbeat` | Yes | Register or refresh an agent |
| `GET` | `/api/v1/agents/{id}/tasks` | Yes | Return pending tasks for a specific agent |

#### Tasks

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/api/v1/tasks` | Yes | Create a new task (called from CI/CD) |
| `GET` | `/api/v1/tasks` | Yes | List tasks (optional `?agent_id=` / `?status=` filters) |
| `GET` | `/api/v1/tasks/{id}` | Yes | Get a single task by ID |
| `POST` | `/api/v1/tasks/{id}/result` | Yes | Agent reports task progress or completion |

### Storage

An in-memory map protected by a `sync.RWMutex`. Intentionally simple: the server is stateless enough that a restart only loses pending tasks. A file-backed or SQLite store can be swapped in later without changing the API (the `store` interface is the boundary).

### Auth middleware

`auth(next http.HandlerFunc) http.HandlerFunc` — reads `Authorization`, strips the `Bearer ` prefix, and compares the token using `crypto/subtle.ConstantTimeCompare`. Unauthorized requests are logged at WARN with the caller's IP before returning HTTP 401.

### Stale agent detection

A background goroutine (`staleSweep`) runs every `agentTimeout / 3` (minimum 2 s) and calls `store.markStaleAgents(agentTimeout)`. Any agent whose `LastHeartbeat` is older than the timeout is set to `status: "offline"` and a WARN is logged. When that agent next sends a heartbeat, it is logged at INFO as "agent back online".

---

## Agent

### CLI flags / environment variables

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--server` / `-s` | `RASPIDEPLOY_SERVER` | — | **Required.** Server base URL |
| `--agent-id` / `--id` | `RASPIDEPLOY_AGENT_ID` | — | **Required.** Unique name for this Pi |
| `--secret` / `-k` | `RASPIDEPLOY_SECRET` | — | **Required.** Shared auth secret |
| `--hostname` / `-n` | `HOSTNAME` | system hostname | Friendly display name |
| `--interval` / `-i` | `RASPIDEPLOY_POLL_INTERVAL` | `30s` | How often to poll |
| `--scripts-dir` / `-S` | `RASPIDEPLOY_SCRIPTS_DIR` | `/etc/raspicd/scripts` | Directory of named scripts |
| `--debug` / `-d` | `RASPIDEPLOY_DEBUG` | `false` | Verbose logging |

### Poll loop

On startup the agent contacts the server **immediately** (before the first tick), so the server registers it as online without delay. Subsequent polls happen on the configured interval.

```
on startup, then every <interval>:
  1. POST /agents/heartbeat          (register + keep-alive)
  2. GET  /agents/{id}/tasks         (fetch pending tasks)
  3. for each task:
       a. POST /tasks/{id}/result    { status: "running" }   ← before executing
       b. execute the task
       c. POST /tasks/{id}/result    { status: "completed|failed", output, error }
```

Step 3a ensures the server shows `running` even if the agent crashes mid-execution, preventing the task from appearing stuck in `pending` forever.

### Task types

#### `named_script` ★ recommended

Runs a pre-installed script by name. No command content travels over the wire.

Payload:
```json
{
  "name":   "deploy-myapp",
  "config": {
    "ref":     "v1.2.3",
    "env":     "production",
    "restart": true
  }
}
```

The agent resolves `name` to `<scripts_dir>/deploy-myapp.sh` and runs it after validation (see Security Model above).

**Environment variables injected into the script:**

| Variable | Value |
|---|---|
| `RASPIDEPLOY_TASK_ID` | ID of this task |
| `RASPIDEPLOY_AGENT_ID` | ID of this agent |
| `RASPIDEPLOY_CONFIG` | Full `config` object as a JSON string |
| `RASPIDEPLOY_CONFIG_<KEY>` | One var per top-level scalar in `config` (string / number / bool) |

Scripts inherit the agent process environment (PATH, HOME, etc.) so standard tools work without explicit configuration.

#### `deploy` — git clone or pull, then run commands

Payload:
```json
{
  "repo_url":   "https://github.com/user/repo.git",
  "ref":        "main",
  "target_dir": "/opt/myapp",
  "commands":   ["make build", "systemctl restart myapp"]
}
```

- If `target_dir/.git` does not exist → `git clone --branch <ref> <repo_url> <target_dir>`
- If it exists → `git fetch --tags --prune origin` + `git checkout -f <ref>` + `git pull --ff-only` (best-effort; silently skipped for tags)
- Each command is run with `bash -c <cmd>` inside `target_dir`; combined stdout+stderr is captured

#### `script` — run an arbitrary shell script

Payload:
```json
{ "script": "apt-get update && apt-get upgrade -y" }
```

Runs `bash -c <script>` in the agent's working directory.

#### `restart` — restart a systemd service

Payload:
```json
{ "service": "myapp" }
```

Runs `systemctl restart <service>`.

---

## Task lifecycle

```
pending  ──▶  running  ──▶  completed
                       ╰──▶  failed
```

`pending → running` is reported by the agent immediately before execution so an operator can see the task was picked up even if the agent crashes mid-run.

---

## Logging conventions

| Event | Level |
|---|---|
| Agent first registration | INFO |
| Repeated heartbeat | DEBUG |
| Agent back online (was offline) | INFO |
| Agent marked offline (no heartbeat) | WARN |
| Task created | INFO |
| Tasks dispatched to agent (>0) | INFO |
| No pending tasks for agent | DEBUG |
| Task running / completed | INFO |
| Task failed | WARN |
| Unauthorized request (401) | WARN |
| Method not allowed | WARN |
| Bad request body | WARN |
| Task not found | WARN |
| Server start / shutdown | INFO |
| `/health` endpoint | silent |

---

## Daemon setup on Raspberry Pi

The agent binary runs as a systemd service. Configuration lives in `/etc/raspicd/agent.env` (mode `600` — it contains the secret). Named scripts live in `/etc/raspicd/scripts/` and must be executable.

```
/etc/systemd/system/raspicd-agent.service   ← unit file
/etc/raspicd/agent.env                       ← configuration (chmod 600)
/etc/raspicd/scripts/<name>.sh               ← named scripts (chmod +x)
```

---

## CI/CD Integration

Trigger a deployment with a single `curl`. The recommended approach uses `named_script` so no raw commands travel over the wire:

```bash
curl -sf -X POST https://your-server/api/v1/tasks \
  -H "Authorization: Bearer $RASPIDEPLOY_SECRET" \
  -H "Content-Type: application/json" \
  -d '{
    "type":     "named_script",
    "agent_id": "raspi-living-room",
    "payload":  {
      "name":   "deploy-myapp",
      "config": {
        "ref": "'$GITHUB_REF_NAME'",
        "env": "production"
      }
    }
  }'
```

See `examples/github-actions/` for ready-to-use workflow files.

---

## Release pipeline (GitHub Actions)

Two workflows live in `.github/workflows/`:

| Workflow | Trigger | What it does |
|---|---|---|
| `ci.yml` | Push / PR | `go vet`, `go test -race`, smoke build |
| `release.yml` | `vX.Y.Z` tag | Publishes agent binaries to GitHub Releases + server Docker image to `ghcr.io` |

Agent binaries produced by the release workflow:

| File | Target |
|---|---|
| `raspicd-agent-linux-arm64` | Pi 3 / 4 / 5 (64-bit OS) |
| `raspicd-agent-linux-armv7` | Pi 2 / 3 (32-bit OS) |
| `raspicd-agent-linux-amd64` | x86-64 (testing / VMs) |

### Docker (Server)

`Dockerfile.server` uses a multi-stage build:

1. `golang:1.22-alpine` builder — accepts `ARG TARGETARCH` from Docker Buildx so the Go cross-compiler is used natively (no QEMU slowness in the build stage)
2. `alpine:3.19` runtime — non-root user, `wget`-based health check on `/health`, exposes `:8080`

The release workflow builds and pushes a multi-arch image (`linux/amd64`, `linux/arm64`) to `ghcr.io` using Docker Buildx with registry-based layer caching.

---

## Build targets (Makefile)

| Target | Description |
|---|---|
| `make build` | Build server + agent for current platform |
| `make build-server` | Build server only |
| `make build-agent` | Build agent for current platform |
| `make build-agent-arm64` | Cross-compile agent for Raspberry Pi (ARM64) |
| `make build-agent-armv7` | Cross-compile agent for older Pi models (ARMv7) |
| `make test` | Run unit tests |
| `make docker-build` | Build the server Docker image |
| `make docker-up` | Start server via docker-compose |
| `make docker-down` | Stop server |
