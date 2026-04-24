# RaspiDeploy — Implementation Plan

## Overview

RaspiDeploy is a lightweight remote-deployment system for Raspberry Pis that sit behind a NAT (no public IP, no exposed ports). It consists of two binaries:

- **Server** — a publicly accessible REST API. CI/CD pipelines push deployment tasks to it. Runs in Docker.
- **Agent** — a daemon installed on each Raspberry Pi. It polls the server, picks up tasks, and executes them locally (git operations + shell commands). No inbound connectivity required.

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

- A single shared secret (`RASPIDEPLOY_SECRET`) is required on both sides.
- Every API request (including agent calls) must carry the header `Authorization: Bearer <secret>`.
- The `/health` endpoint is unauthenticated (used for Docker health checks and uptime monitoring).
- In a future iteration the secret can be rotated per-agent, but a global secret is sufficient for v1.

---

## Project Structure

```
raspideploy/
├── cmd/
│   ├── server/
│   │   └── main.go          # Server entrypoint (CLI flags, signal handling)
│   └── agent/
│       └── main.go          # Agent entrypoint (CLI flags, main poll loop)
├── internal/
│   ├── server/
│   │   ├── server.go        # HTTP server, route setup, auth middleware
│   │   ├── handlers.go      # One handler per route group
│   │   └── store.go         # In-memory store (agents, tasks) behind an interface
│   ├── agent/
│   │   ├── client.go        # HTTP client — heartbeat, fetch tasks, report results
│   │   └── executor.go      # Task execution logic
│   ├── models/
│   │   └── models.go        # Shared types: Task, Agent, TaskStatus, payloads
│   └── utils/
│       └── logger.go        # Structured logger (logrus)
├── Dockerfile.server        # Multi-stage build, non-root user
├── docker-compose.yml       # Server + named volume for persistence
├── Makefile                 # build, build-arm, test, docker-* targets
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
| `--data-dir` / `-d` | `RASPIDEPLOY_DATA_DIR` | `./data` | Directory for persistence (future use) |
| `--debug` / `-D` | `RASPIDEPLOY_DEBUG` | `false` | Verbose logging |

### REST API

All routes except `/health` require `Authorization: Bearer <secret>`.

#### Health

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/health` | No | Returns `{"status":"healthy"}` |

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
| `GET` | `/api/v1/tasks` | Yes | List all tasks (with optional `?agent_id=` / `?status=` filters) |
| `GET` | `/api/v1/tasks/{id}` | Yes | Get a single task by ID |
| `POST` | `/api/v1/tasks/{id}/result` | Yes | Agent reports task progress or completion |

### Storage (v1)

An in-memory map protected by a `sync.RWMutex`. This is intentionally simple: the server is stateless enough that a restart only loses pending tasks. A file-backed or SQLite store can be added later without changing the API.

### Auth middleware

A single `auth(next http.HandlerFunc) http.HandlerFunc` wrapper. It reads `Authorization`, strips the `Bearer ` prefix, and compares the token to the configured secret using `subtle.ConstantTimeCompare` to avoid timing attacks.

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
| `--debug` / `-d` | `RASPIDEPLOY_DEBUG` | `false` | Verbose logging |

### Poll loop

```
every <interval>:
  1. POST /agents/heartbeat          (register + keep-alive)
  2. GET  /agents/{id}/tasks         (fetch pending tasks)
  3. for each task:
       a. POST /tasks/{id}/result    { status: "running" }
       b. execute the task
       c. POST /tasks/{id}/result    { status: "completed|failed", output, error }
```

### Task types

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

Logic:
- If `target_dir/.git` does not exist → `git clone --branch <ref> <repo_url> <target_dir>`
- If it exists → `git fetch --tags origin` + `git checkout <ref>` + `git pull --ff-only`
- Run each command with `bash -c <cmd>` inside `target_dir`, collecting stdout+stderr

#### `script` — run an arbitrary shell script

Payload:
```json
{
  "script": "apt-get update && apt-get upgrade -y"
}
```

Runs `bash -c <script>` in the agent's working directory.

#### `restart` — restart a systemd service

Payload:
```json
{
  "service": "myapp"
}
```

Runs `systemctl restart <service>`.

---

## Task lifecycle

```
pending  ──▶  running  ──▶  completed
                       ╰──▶  failed
```

The transition `pending → running` is reported by the agent immediately before execution. This lets an operator watching the server know a task was picked up even if the agent crashes mid-execution.

---

## Daemon setup on Raspberry Pi

The agent binary is intended to run as a systemd service. A sample unit file will be provided:

```
/etc/systemd/system/raspideploy-agent.service
```

The service reads all configuration from environment variables via a `EnvironmentFile=/etc/raspideploy/agent.env` directive, so secrets are not on the command line.

---

## CI/CD Integration

From any CI/CD pipeline, a deployment is triggered with a single `curl`:

```bash
curl -s -X POST https://your-server/api/v1/tasks \
  -H "Authorization: Bearer $RASPIDEPLOY_SECRET" \
  -H "Content-Type: application/json" \
  -d '{
    "type":     "deploy",
    "node_id":  "raspi-living-room",
    "payload":  {
      "repo_url":   "https://github.com/user/repo.git",
      "ref":        "'$GITHUB_REF_NAME'",
      "target_dir": "/opt/myapp",
      "commands":   ["make install", "systemctl restart myapp"]
    }
  }'
```

The secret is stored as a CI/CD secret variable (`RASPIDEPLOY_SECRET`). No other setup is required.

---

## Docker (Server)

`Dockerfile.server` — multi-stage build:
1. `golang:1.21-alpine` builder stage compiles the server binary
2. `alpine:3.18` runtime stage, non-root user, exposes `:8080`

`docker-compose.yml` provides:
- The server container with `RASPIDEPLOY_SECRET` from the host environment
- A named volume mounted at `/data` for the data directory
- A health check against `GET /health`

---

## Build targets (Makefile)

| Target | Description |
|---|---|
| `make build` | Build server + agent for current platform |
| `make build-agent-arm64` | Cross-compile agent for Raspberry Pi (ARM64) |
| `make build-agent-armv7` | Cross-compile agent for older Pi models (ARMv7) |
| `make test` | Run unit tests |
| `make docker-build` | Build the server Docker image |
| `make docker-up` | Start server via docker-compose |
| `make docker-down` | Stop server |

---

## Implementation order

1. **Models** — `internal/models/models.go` (Task, Agent, payloads, status constants)
2. **Logger** — `internal/utils/logger.go`
3. **Server store** — `internal/server/store.go` (in-memory, behind interface)
4. **Server handlers + auth** — `internal/server/server.go` + `handlers.go`
5. **Server entrypoint** — `cmd/server/main.go`
6. **Agent client** — `internal/agent/client.go`
7. **Agent executor** — `internal/agent/executor.go`
8. **Agent entrypoint** — `cmd/agent/main.go`
9. **Dockerfile + docker-compose**
10. **Makefile**
11. **Systemd unit file example**
