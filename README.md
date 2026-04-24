# RasPiCD

Deploy to Raspberry Pis from any CI/CD pipeline.

**How it works:** a lightweight server sits on the public internet. Each Pi runs an agent that maintains a persistent long-poll connection to the server. Your CI/CD pipeline pushes a task — the agent receives it instantly and executes it locally.

No inbound ports are needed on the Pi. The agent connects outbound only.

```
CI/CD pipeline  ──POST /api/v1/tasks──▶  Server (public)
                                              ▲
                          Agent (Pi) ─────────┘
                          long-poll: task delivered in milliseconds
```

---

## Requirements

| Component | Requirements |
|-----------|-------------|
| Server    | Any Linux host with Docker + Docker Compose |
| Agent     | Raspberry Pi running Linux, `git` and `bash` installed |

---

## 1. Deploy the Server

### Generate secrets

RasPiCD uses two separate secrets so a compromised Pi cannot be used to create new tasks.

```bash
openssl rand -hex 32   # CI/CD secret  → RASPIDEPLOY_SECRET
openssl rand -hex 32   # Agent secret  → RASPIDEPLOY_AGENT_SECRET
```

| Secret | Used by | Can do |
|--------|---------|--------|
| `RASPIDEPLOY_SECRET` | CI/CD pipelines | Create tasks, list tasks and agents |
| `RASPIDEPLOY_AGENT_SECRET` | Agents on each Pi | Heartbeat, fetch tasks, report results |

### Run with Docker Compose

Create a `docker-compose.yml` on your server host:

```yaml
services:
  raspicd-server:
    image: ghcr.io/your-org/raspicd:latest
    container_name: raspicd-server
    restart: unless-stopped
    environment:
      RASPIDEPLOY_SECRET: "${RASPIDEPLOY_SECRET}"
      RASPIDEPLOY_AGENT_SECRET: "${RASPIDEPLOY_AGENT_SECRET}"
    ports:
      - "8080:8080"
    volumes:
      - raspicd-data:/data

volumes:
  raspicd-data:
```

Then start it:

```bash
export RASPIDEPLOY_SECRET=<your-ci-secret>
export RASPIDEPLOY_AGENT_SECRET=<your-agent-secret>
docker compose up -d
```

The server listens on port `8080`. Put a reverse proxy (nginx, Caddy) in front of it to terminate TLS.

**Verify it is running:**

```bash
curl https://your-server.example.com/health
# {"status":"healthy"}
```

### Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `RASPIDEPLOY_SECRET` | Yes | — | CI/CD Bearer token secret (used by pipelines to create tasks) |
| `RASPIDEPLOY_AGENT_SECRET` | Yes | — | Agent Bearer token secret (used by agents to poll and report) |
| `RASPIDEPLOY_BIND` | No | `:8080` | Listen address |
| `RASPIDEPLOY_AGENT_TIMEOUT` | No | `90s` | Mark agents offline after this duration without a heartbeat |
| `RASPIDEPLOY_DEBUG` | No | `false` | Verbose logging |

### Expose the server publicly

Example Caddy configuration:

```
your-server.example.com {
    reverse_proxy localhost:8080
}
```

---

## 2. Install an Agent on a Raspberry Pi

### Download the binary

Pre-built binaries for every release are attached to the [GitHub Releases](../../releases) page.

On the Pi, download the binary that matches your architecture:

```bash
# Pi 3 / 4 / 5 running a 64-bit OS (most common)
VERSION=v0.1.0
curl -fsSL -o /tmp/raspicd-agent \
  "https://github.com/your-org/raspicd/releases/download/${VERSION}/raspicd-agent-linux-arm64"

# Pi 2 / 3 running a 32-bit OS
curl -fsSL -o /tmp/raspicd-agent \
  "https://github.com/your-org/raspicd/releases/download/${VERSION}/raspicd-agent-linux-armv7"

sudo mv /tmp/raspicd-agent /usr/local/bin/raspicd-agent
sudo chmod +x /usr/local/bin/raspicd-agent
```

### Configure the agent

Create the config directory and write the environment file:

```bash
sudo mkdir -p /etc/raspicd
sudo tee /etc/raspicd/agent.env > /dev/null <<EOF
RASPIDEPLOY_SERVER=https://your-server.example.com
RASPIDEPLOY_AGENT_ID=raspi-living-room
RASPIDEPLOY_AGENT_SECRET=<your-agent-secret>
# RASPIDEPLOY_SCRIPTS_DIR=/etc/raspicd/scripts
# RASPIDEPLOY_POLL_INTERVAL=60s   # max retry delay – exponential backoff (default: 60s)
EOF

# Protect the file — it contains the secret
sudo chmod 600 /etc/raspicd/agent.env
```

`RASPIDEPLOY_AGENT_ID` is the unique name you will use when targeting this Pi from CI/CD. Use something descriptive (`raspi-living-room`, `raspi-garage`, etc.).

### Agent environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `RASPIDEPLOY_SERVER` | Yes | — | Server base URL |
| `RASPIDEPLOY_AGENT_ID` | Yes | — | Unique name for this Pi |
| `RASPIDEPLOY_AGENT_SECRET` | Yes | — | Agent Bearer token secret |
| `RASPIDEPLOY_POLL_INTERVAL` | No | `60s` | Maximum retry delay (exponential backoff: 1s → 2s → 4s → … → this value) |
| `RASPIDEPLOY_SCRIPTS_DIR` | No | `/etc/raspicd/scripts` | Directory of named scripts |
| `RASPIDEPLOY_DEBUG` | No | `false` | Verbose logging |

### Run as a systemd daemon

Systemd keeps the agent running across reboots and restarts it automatically if it crashes.

#### 1. Create the unit file

```bash
sudo tee /etc/systemd/system/raspicd-agent.service > /dev/null <<'EOF'
[Unit]
Description=RasPiCD Agent
# Wait for the network before starting — important on Pi which may
# take a few seconds to get an IP after boot.
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
# Change to the user that should run the agent.
# On Raspberry Pi OS the default user is "pi".
User=pi
EnvironmentFile=/etc/raspicd/agent.env
ExecStart=/usr/local/bin/raspicd-agent
# Restart on unexpected exit (crashes, OOM kills).
# The agent handles server reconnects internally with exponential backoff,
# so this covers only hard failures.
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
```

> The unit file is also available at [`deploy/agent.service`](deploy/agent.service) in this repository.

#### 2. Enable and start

```bash
sudo systemctl daemon-reload
sudo systemctl enable raspicd-agent   # start automatically on boot
sudo systemctl start raspicd-agent
```

#### 3. Verify it is running

```bash
sudo systemctl status raspicd-agent
```

Expected output:

```
● raspicd-agent.service - RasPiCD Agent
     Loaded: loaded (/etc/systemd/system/raspicd-agent.service; enabled)
     Active: active (running) since ...
```

#### 4. View live logs

```bash
# Follow logs in real time
sudo journalctl -u raspicd-agent -f

# Last 50 lines
sudo journalctl -u raspicd-agent -n 50
```

#### 5. Lifecycle commands

| Action | Command |
|--------|---------|
| Start | `sudo systemctl start raspicd-agent` |
| Stop (graceful) | `sudo systemctl stop raspicd-agent` |
| Restart | `sudo systemctl restart raspicd-agent` |
| Disable autostart | `sudo systemctl disable raspicd-agent` |
| Reload unit file | `sudo systemctl daemon-reload` |

Stopping the agent with `systemctl stop` sends SIGTERM — the agent finishes any running task, notifies the server it is going offline, and exits cleanly.

The agent connects on startup and maintains a persistent long-poll connection — tasks are delivered in milliseconds. If the connection drops the agent reconnects automatically using exponential backoff (1s, 2s, 4s … up to `RASPIDEPLOY_POLL_INTERVAL`, default 60s) with ±25% jitter to avoid thundering herds. When stopped gracefully (e.g. `systemctl stop` or CTRL+C), the agent notifies the server immediately and its status switches to offline.

### Set up named scripts

Named scripts are the recommended way to deploy (see [Task types](#task-types) below). Create the scripts directory and add executable shell scripts to it:

```bash
sudo mkdir -p /etc/raspicd/scripts

sudo tee /etc/raspicd/scripts/deploy-myapp.sh > /dev/null <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

REF="${RASPIDEPLOY_CONFIG_REF:-main}"
TARGET_DIR="/opt/myapp"

cd "$TARGET_DIR"
git fetch --tags --prune origin
git checkout -f "$REF"
git pull --ff-only || true

make build
systemctl restart myapp
echo "Deployed $REF successfully"
EOF

sudo chmod +x /etc/raspicd/scripts/deploy-myapp.sh
```

Scripts receive task context as environment variables — no arguments needed:

| Variable | Value |
|----------|-------|
| `RASPIDEPLOY_TASK_ID` | ID of this task |
| `RASPIDEPLOY_AGENT_ID` | ID of this agent |
| `RASPIDEPLOY_CONFIG` | Full `config` object as a JSON string |
| `RASPIDEPLOY_CONFIG_<KEY>` | One var per top-level scalar in `config` |

See [`examples/named-scripts/`](examples/named-scripts/) for a fully annotated example.

---

## 3. Trigger a Deployment from CI/CD

### Task types

#### `named_script` — run a pre-installed script by name (recommended)

The most secure option. CI/CD sends only a script name; the actual script lives on the Pi. No raw commands travel over the wire.

```json
{
  "type":     "named_script",
  "agent_id": "raspi-living-room",
  "payload": {
    "name": "deploy-myapp",
    "config": {
      "ref": "v1.2.3",
      "env": "production"
    }
  }
}
```

The agent resolves `name` to `/etc/raspicd/scripts/deploy-myapp.sh` and validates it before executing:
- Name must match `[a-zA-Z0-9_-]+` (no path traversal)
- Script must exist in the scripts directory
- Script must have the execute bit set (`chmod +x`)

#### `deploy` — clone or update a git repository, then run commands

```json
{
  "type": "deploy",
  "agent_id": "raspi-living-room",
  "payload": {
    "repo_url":   "https://github.com/you/myapp.git",
    "ref":        "main",
    "target_dir": "/opt/myapp",
    "commands":   [
      "make build",
      "systemctl restart myapp"
    ]
  }
}
```

If `target_dir` does not contain a git repository, a fresh clone is performed. Otherwise the repo is fetched and the requested ref is checked out.

#### `script` — run an arbitrary shell script

```json
{
  "type": "script",
  "agent_id": "raspi-living-room",
  "payload": {
    "script": "apt-get update && apt-get upgrade -y"
  }
}
```

#### `restart` — restart a systemd service

```json
{
  "type": "restart",
  "agent_id": "raspi-living-room",
  "payload": {
    "service": "myapp"
  }
}
```

### Broadcast to all online agents

Use `POST /api/v1/tasks/broadcast` to send the same task to every agent that is currently online. The server fans out one task per agent and returns all task IDs:

```bash
curl -X POST https://your-server.example.com/api/v1/tasks/broadcast \
  -H "Authorization: Bearer $RASPIDEPLOY_SECRET" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "named_script",
    "payload": {
      "name": "deploy-myapp",
      "config": { "ref": "v1.2.3" }
    }
  }'
# [{"agent_id":"raspi-living-room","task_id":"abc123"},{"agent_id":"raspi-garage","task_id":"def456"}]
```

See [`examples/github-actions/write-commit-id.yml`](examples/github-actions/write-commit-id.yml) for a full broadcast workflow that polls all returned task IDs.

### GitHub Actions

Add `RASPIDEPLOY_SECRET` and `RASPIDEPLOY_SERVER` as repository secrets. A ready-to-use workflow is available at [`examples/github-actions/deploy-named-script.yml`](examples/github-actions/deploy-named-script.yml).

Minimal example using `named_script`:

```yaml
- name: Deploy to Raspberry Pi
  run: |
    curl -sf -X POST "$RASPIDEPLOY_SERVER/api/v1/tasks" \
      -H "Authorization: Bearer $RASPIDEPLOY_SECRET" \
      -H "Content-Type: application/json" \
      -d '{
        "type":     "named_script",
        "agent_id": "raspi-living-room",
        "payload": {
          "name": "deploy-myapp",
          "config": {
            "ref": "${{ github.ref_name }}",
            "env": "production"
          }
        }
      }'
  env:
    RASPIDEPLOY_SERVER: ${{ secrets.RASPIDEPLOY_SERVER }}
    RASPIDEPLOY_SECRET: ${{ secrets.RASPIDEPLOY_SECRET }}
```

### GitLab CI

Add `RASPIDEPLOY_SECRET` and `RASPIDEPLOY_SERVER` as CI/CD variables, then:

```yaml
deploy:
  stage: deploy
  script:
    - |
      curl -sf -X POST "$RASPIDEPLOY_SERVER/api/v1/tasks" \
        -H "Authorization: Bearer $RASPIDEPLOY_SECRET" \
        -H "Content-Type: application/json" \
        -d '{
          "type":     "named_script",
          "agent_id": "raspi-living-room",
          "payload": {
            "name": "deploy-myapp",
            "config": {
              "ref": "'$CI_COMMIT_REF_NAME'",
              "env": "production"
            }
          }
        }'
  only:
    - main
```

### Plain curl

```bash
curl -X POST https://your-server.example.com/api/v1/tasks \
  -H "Authorization: Bearer $RASPIDEPLOY_SECRET" \
  -H "Content-Type: application/json" \
  -d '{
    "type":     "named_script",
    "agent_id": "raspi-living-room",
    "payload": {
      "name": "deploy-myapp",
      "config": {
        "ref": "v1.2.3",
        "env": "production"
      }
    }
  }'
```

---

## 4. Monitor Tasks

**List all tasks:**

```bash
curl -s https://your-server.example.com/api/v1/tasks \
  -H "Authorization: Bearer $RASPIDEPLOY_SECRET" | jq .
```

**Filter by agent or status:**

```bash
curl -s "https://your-server.example.com/api/v1/tasks?agent_id=raspi-living-room&status=failed" \
  -H "Authorization: Bearer $RASPIDEPLOY_SECRET" | jq .
```

**Poll a specific task until it completes:**

```bash
TASK_ID=2c52c5e6c0880ff8

while true; do
  STATUS=$(curl -s "https://your-server.example.com/api/v1/tasks/$TASK_ID" \
    -H "Authorization: Bearer $RASPIDEPLOY_SECRET" | jq -r .status)
  echo "status: $STATUS"
  [[ "$STATUS" == "completed" || "$STATUS" == "failed" ]] && break
  sleep 5
done
```

**List all registered agents:**

```bash
curl -s https://your-server.example.com/api/v1/agents \
  -H "Authorization: Bearer $RASPIDEPLOY_SECRET" | jq .
```

---

## API Reference

Endpoints are split by which secret they require.

**Unauthenticated**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Server health check |

**Agent secret** (`RASPIDEPLOY_AGENT_SECRET`)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/agents/heartbeat` | Agent registration/keep-alive |
| `POST` | `/api/v1/agents/{id}/disconnect` | Agent graceful shutdown notification |
| `GET` | `/api/v1/agents/{id}/tasks` | Pending tasks for one agent (supports `?wait=1` for long polling) |
| `POST` | `/api/v1/tasks/{id}/result` | Agent reports task progress/completion |

**CI secret** (`RASPIDEPLOY_SECRET`)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/agents` | List registered agents |
| `POST` | `/api/v1/tasks` | Create a task for a specific agent |
| `POST` | `/api/v1/tasks/broadcast` | Create the same task for all online agents |
| `GET` | `/api/v1/tasks` | List tasks (`?agent_id=`, `?status=`) |
| `GET` | `/api/v1/tasks/{id}` | Get a single task |

### Response headers

Every response from the server includes:

| Header | Example | Description |
|--------|---------|-------------|
| `X-RasPiCD-Version` | `v1.2.3` | Server binary version — useful for verifying which build is running |

```bash
curl -sI https://your-server.example.com/health
# X-RasPiCD-Version: v1.2.3
```

### Task statuses

| Status | Meaning |
|--------|---------|
| `pending` | Waiting to be picked up by the agent |
| `running` | Agent has started execution |
| `completed` | Execution finished successfully |
| `failed` | Execution finished with an error |

---

## Building from Source

```bash
git clone https://github.com/you/raspicd.git
cd raspicd
go mod download

make build              # server + agent for current platform
make build-agent-arm64  # agent for Pi 3/4/5 (64-bit)
make build-agent-armv7  # agent for Pi 2/3 (32-bit)
make docker-build       # build server Docker image
```

Requires Go 1.22+.

### Versioning

The version string is injected at link time from the nearest git tag:

| Build command | Version value |
|---------------|--------------|
| `make build` on a tagged commit (`v1.2.3`) | `v1.2.3` |
| `make build` after additional commits | `v1.2.3-5-gabcdef` |
| `make build` with uncommitted changes | `v1.2.3-dirty` |
| `go build ./...` (no Makefile) | `dev` |
| `make build VERSION=v9.9.9` | `v9.9.9` (manual override) |

The version appears in startup logs and is exposed on every HTTP response via the `X-RasPiCD-Version` header.
