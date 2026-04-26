# RasPi CD

Deploy to Raspberry Pis (or any other host) from any CI/CD pipeline.

**How it works:** a lightweight server sits on the public internet. Each Pi runs an agent that holds a persistent **WebSocket** connection to the server. Your CI/CD pipeline pushes a task — the server delivers it instantly over the open socket and the agent executes it locally.

No inbound ports are needed on the Pi. The agent connects outbound only.

```
CI/CD pipeline  ──POST /api/v1/tasks──▶  Server (public)
                                              │
                          Agent (Pi) ◀────────┘
                          WebSocket: task delivered in milliseconds
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
openssl rand -hex 32   # CI/CD secret  → RASPICD_SECRET
openssl rand -hex 32   # Agent secret  → RASPICD_AGENT_SECRET
```

| Secret | Used by | Can do |
|--------|---------|--------|
| `RASPICD_SECRET` | CI/CD pipelines | Create tasks, list tasks and agents |
| `RASPICD_AGENT_SECRET` | Agents on each Pi | Heartbeat, fetch tasks, report results |

### Quick install (Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/simonoche/raspi-cd/main/scripts/install-server.sh | sudo bash
```

The script auto-detects your architecture, downloads the latest binary, generates all secrets, writes `/etc/raspicd/server.env`, and starts a systemd service. **The generated keys are printed at the end — save them.**

---

### Option A — Run with Docker Compose

Create a `docker-compose.yml` on your server host:

```yaml
services:
  raspicd-server:
    image: ghcr.io/your-org/raspicd:latest
    container_name: raspicd-server
    restart: unless-stopped
    environment:
      RASPICD_SECRET: "${RASPICD_SECRET}"
      RASPICD_AGENT_SECRET: "${RASPICD_AGENT_SECRET}"
      RASPICD_SIGNING_KEY: "${RASPICD_SIGNING_KEY}"
    ports:
      - "8080:8080"
    volumes:
      - raspicd-data:/data

volumes:
  raspicd-data:
```

Then start it:

```bash
export RASPICD_SECRET=<your-ci-secret>
export RASPICD_AGENT_SECRET=<your-agent-secret>
docker compose up -d
```

The server listens on port `8080`. Put a reverse proxy (nginx, Caddy) in front of it to terminate TLS.

### Option B — .deb package (Debian / Ubuntu / Raspberry Pi OS)

.deb packages for `amd64` and `arm64` are attached to every [GitHub Release](../../releases).

```bash
# Replace with the actual version and architecture
VERSION=v0.1.0
ARCH=amd64   # or arm64
wget "https://github.com/simonoche/raspi-cd/releases/download/${VERSION}/raspicd-server_${VERSION#v}_${ARCH}.deb"
sudo dpkg -i "raspicd-server_${VERSION#v}_${ARCH}.deb"
```

The post-install script automatically:
- Creates a `raspicd` system user
- Generates `RASPICD_SECRET`, `RASPICD_AGENT_SECRET`, and the Ed25519 signing keypair
- Writes `/etc/raspicd/server.env`
- Enables and starts the `raspicd-server` systemd service
- **Prints all generated keys** at the end — copy them to your agents

### Option C — Manual binary

Pre-built binaries for Linux and macOS are on the [GitHub Releases](../../releases) page.

```bash
VERSION=v0.1.0
BASE="https://github.com/simonoche/raspi-cd/releases/download/${VERSION}"

# Linux x86-64 (most servers / VMs)
curl -fsSL -o /usr/local/bin/raspicd-server "${BASE}/raspicd-server-linux-amd64"
chmod +x /usr/local/bin/raspicd-server
```

Generate secrets and start:

```bash
export RASPICD_SECRET=$(openssl rand -hex 32)
export RASPICD_AGENT_SECRET=$(openssl rand -hex 32)
export RASPICD_SIGNING_KEY=$(openssl rand -hex 32)
raspicd-server
```

**Verify it is running:**

```bash
curl https://your-server.example.com/health
# {"status":"healthy"}
```

### Environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `RASPICD_SECRET` | Yes | — | CI/CD Bearer token secret (used by pipelines to create tasks) |
| `RASPICD_AGENT_SECRET` | Yes | — | Agent Bearer token secret (used by agents to poll and report) |
| `RASPICD_SIGNING_KEY` | Yes* | — | Ed25519 private key seed as 64 hex chars. See [Task signing](#task-signing) |
| `RASPICD_DATA_FILE` | No | `/data/store.json` | Path to the JSON file used to persist tasks and agents across restarts |
| `RASPICD_BIND` | No | `:8080` | Listen address |
| `RASPICD_AGENT_TIMEOUT` | No | `90s` | Mark agents offline after this duration without a heartbeat |
| `RASPICD_DEBUG` | No | `false` | Verbose logging |

*If omitted, an ephemeral key is generated at startup — tasks can't be verified after a server restart.

### Expose the server publicly

Example Caddy configuration:

```
your-server.example.com {
    reverse_proxy localhost:8080
}
```

---

## 2. Install an Agent on a Raspberry Pi

### Quick install (Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/simonoche/raspi-cd/main/scripts/install-agent.sh | sudo bash
```

The script detects your architecture, downloads the latest binary, prompts for your server URL and secrets, and starts a systemd service.

### .deb package (Debian / Ubuntu / Raspberry Pi OS)

```bash
VERSION=v0.1.0
ARCH=arm64   # arm64 (Pi 3/4/5 64-bit), amd64, or armhf (Pi 2/3 32-bit)
wget "https://github.com/simonoche/raspi-cd/releases/download/${VERSION}/raspicd-agent_${VERSION#v}_${ARCH}.deb"
sudo dpkg -i "raspicd-agent_${VERSION#v}_${ARCH}.deb"
```

The post-install script interactively prompts for your server URL, agent ID, and secrets, then creates `/etc/raspicd/agent.env` and starts the `raspicd-agent` systemd service.

### Manual binary download

Pre-built binaries for every release are attached to the [GitHub Releases](../../releases) page.

Download the binary that matches your platform:

```bash
VERSION=v0.1.0
BASE="https://github.com/your-org/raspicd/releases/download/${VERSION}"

# Raspberry Pi 3 / 4 / 5 — 64-bit OS (most common)
curl -fsSL -o /tmp/raspicd-agent "${BASE}/raspicd-agent-linux-arm64"

# Raspberry Pi 2 / 3 — 32-bit OS
curl -fsSL -o /tmp/raspicd-agent "${BASE}/raspicd-agent-linux-armv7"

# Linux x86-64 (VMs, servers)
curl -fsSL -o /tmp/raspicd-agent "${BASE}/raspicd-agent-linux-amd64"

# macOS — Apple Silicon (M1/M2/M3/M4)
curl -fsSL -o /tmp/raspicd-agent "${BASE}/raspicd-agent-darwin-arm64"

# macOS — Intel
curl -fsSL -o /tmp/raspicd-agent "${BASE}/raspicd-agent-darwin-amd64"
```

```bash
sudo mv /tmp/raspicd-agent /usr/local/bin/raspicd-agent
sudo chmod +x /usr/local/bin/raspicd-agent
```

> **macOS only:** Gatekeeper will quarantine unsigned binaries downloaded via curl.
> Remove the quarantine attribute before running:
> ```bash
> xattr -d com.apple.quarantine /usr/local/bin/raspicd-agent
> ```

### Configure the agent

Create the config directory and write the environment file:

```bash
sudo mkdir -p /etc/raspicd
sudo tee /etc/raspicd/agent.env > /dev/null <<EOF
RASPICD_SERVER=https://your-server.example.com
RASPICD_AGENT_ID=raspi-living-room
RASPICD_AGENT_SECRET=<your-agent-secret>
# RASPICD_SCRIPTS_DIR=/etc/raspicd/scripts
# RASPICD_POLL_INTERVAL=60s   # max retry delay – exponential backoff (default: 60s)
EOF

# Protect the file — it contains the secret
sudo chmod 600 /etc/raspicd/agent.env
```

`RASPICD_AGENT_ID` is the unique name you will use when targeting this Pi from CI/CD. Use something descriptive (`raspi-living-room`, `raspi-garage`, etc.).

### Agent environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `RASPICD_SERVER` | Yes | — | Server base URL |
| `RASPICD_AGENT_ID` | Yes | — | Unique name for this Pi |
| `RASPICD_AGENT_SECRET` | Yes | — | Agent Bearer token secret |
| `RASPICD_VERIFY_KEY` | Yes* | — | Ed25519 public key as 64 hex chars. See [Task signing](#task-signing) |
| `RASPICD_POLL_INTERVAL` | No | `60s` | Maximum reconnect delay (exponential backoff: 1s → 2s → 4s → … → this value) |
| `RASPICD_SCRIPTS_DIR` | No | `/etc/raspicd/scripts` | Directory of named scripts |
| `RASPICD_DEBUG` | No | `false` | Verbose logging |

*If omitted, signature verification is skipped with a warning — strongly recommended in production.

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

Stopping the agent with `systemctl stop` sends SIGTERM — the agent sends a WebSocket close frame, finishes any running task, and exits cleanly. The server marks it offline immediately.

The agent connects on startup and holds a persistent **WebSocket** connection — tasks are pushed by the server and received in milliseconds. If the connection drops the agent reconnects automatically using exponential backoff (1s, 2s, 4s … up to `RASPICD_POLL_INTERVAL`, default 60s) with ±25% jitter to avoid thundering herds. Any tasks created while the agent was offline are flushed to it as soon as it reconnects.

### Set up scripts

Scripts live on the Pi and are triggered by name from CI/CD. No code travels over the wire — only a name and optional config data.

#### Directory layout

The default scripts directory is `/etc/raspicd/scripts/` (configurable via `RASPICD_SCRIPTS_DIR`). Subdirectories are supported — use `/` in the script name to address them:

```
/etc/raspicd/scripts/
  deploy-myapp.sh          ← "script": "deploy-myapp"
  deploy-myapp.user        ← optional: run as this OS user
  myapp/
    build.sh               ← "script": "myapp/build"
    restart.sh             ← "script": "myapp/restart"
    restart.user           ← optional: run as this OS user
```

Script names must match `[a-zA-Z0-9_-]` segments separated by `/`. Path traversal (`..`) is blocked.

#### Create a script

```bash
sudo mkdir -p /etc/raspicd/scripts

sudo tee /etc/raspicd/scripts/deploy-myapp.sh > /dev/null <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

REF="${RASPICD_CONFIG_REF:-main}"
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
| `RASPICD_TASK_ID` | ID of this task |
| `RASPICD_AGENT_ID` | ID of this agent |
| `RASPICD_CONFIG` | Full `config` object as a JSON string |
| `RASPICD_CONFIG_<KEY>` | One var per top-level scalar in `config` |

#### Run a script as a specific user

Place a `.user` file next to the script containing the OS username. The agent will run the script via `sudo -E -u <user>`:

```bash
echo "www-data" | sudo tee /etc/raspicd/scripts/deploy-myapp.user
```

The agent must be allowed to sudo as that user without a password. Add a sudoers rule for each script that needs it:

```bash
sudo visudo -f /etc/sudoers.d/raspicd
```

```
# Allow the raspicd agent (running as pi) to execute specific scripts as www-data
pi ALL=(www-data) NOPASSWD: /etc/raspicd/scripts/deploy-myapp.sh

# To also preserve environment variables (RASPICD_* vars):
Defaults!/etc/raspicd/scripts/deploy-myapp.sh env_keep += "RASPICD_TASK_ID RASPICD_AGENT_ID RASPICD_CONFIG"
```

> The `-E` flag passed to sudo requests environment preservation. Whether it is honoured depends on your sudoers `env_reset` / `env_keep` policy.

See [`examples/named-scripts/`](examples/named-scripts/) for a fully annotated example.

---

## 3. Trigger a Deployment from CI/CD

### Run a script

Send the name of a script to run and an optional `config` object. The agent resolves the name to a file on the Pi and never receives raw commands.

```json
{
  "agent_id": "raspi-living-room",
  "script":   "deploy-myapp",
  "config": {
    "ref": "v1.2.3",
    "env": "production"
  }
}
```

The agent validates the name before running:
- Must match `[a-zA-Z0-9_-]+` (no path traversal)
- Script must exist in the scripts directory (`/etc/raspicd/scripts/`)
- Script must have the execute bit set (`chmod +x`)

### Broadcast to all online agents

Use `POST /api/v1/tasks/broadcast` to send the same task to every agent that is currently online. The server fans out one task per agent and returns all task IDs:

```bash
curl -X POST https://your-server.example.com/api/v1/tasks/broadcast \
  -H "Authorization: Bearer $RASPICD_SECRET" \
  -H "Content-Type: application/json" \
  -d '{
    "script": "deploy-myapp",
    "config": { "ref": "v1.2.3" }
  }'
# [{"agent_id":"raspi-living-room","task_id":"abc123"},{"agent_id":"raspi-garage","task_id":"def456"}]
```

See [`examples/github-actions/write-commit-id.yml`](examples/github-actions/write-commit-id.yml) for a full broadcast workflow that polls all returned task IDs.

### GitHub Actions

Add `RASPICD_SECRET` and `RASPICD_SERVER` as repository secrets. Ready-to-use workflows are in [`examples/github-actions/`](examples/github-actions/).

Minimal example:

```yaml
- name: Deploy to Raspberry Pi
  run: |
    curl -sf -X POST "$RASPICD_SERVER/api/v1/tasks" \
      -H "Authorization: Bearer $RASPICD_SECRET" \
      -H "Content-Type: application/json" \
      -d '{
        "agent_id": "raspi-living-room",
        "script":   "deploy-myapp",
        "config": {
          "ref": "${{ github.ref_name }}",
          "env": "production"
        }
      }'
  env:
    RASPICD_SERVER: ${{ secrets.RASPICD_SERVER }}
    RASPICD_SECRET: ${{ secrets.RASPICD_SECRET }}
```

### GitLab CI

Add `RASPICD_SECRET` and `RASPICD_SERVER` as CI/CD variables, then:

```yaml
deploy:
  stage: deploy
  script:
    - |
      curl -sf -X POST "$RASPICD_SERVER/api/v1/tasks" \
        -H "Authorization: Bearer $RASPICD_SECRET" \
        -H "Content-Type: application/json" \
        -d '{
          "agent_id": "raspi-living-room",
          "script":   "deploy-myapp",
          "config": {
            "ref": "'$CI_COMMIT_REF_NAME'",
            "env": "production"
          }
        }'
  only:
    - main
```

### Plain curl

```bash
curl -X POST https://your-server.example.com/api/v1/tasks \
  -H "Authorization: Bearer $RASPICD_SECRET" \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "raspi-living-room",
    "script":   "deploy-myapp",
    "config": {
      "ref": "v1.2.3",
      "env": "production"
    }
  }'
```

---

## 4. Monitor Tasks

**List all tasks:**

```bash
curl -s https://your-server.example.com/api/v1/tasks \
  -H "Authorization: Bearer $RASPICD_SECRET" | jq .
```

**Filter by agent or status:**

```bash
curl -s "https://your-server.example.com/api/v1/tasks?agent_id=raspi-living-room&status=failed" \
  -H "Authorization: Bearer $RASPICD_SECRET" | jq .
```

**Poll a specific task until it completes:**

```bash
TASK_ID=2c52c5e6c0880ff8

while true; do
  STATUS=$(curl -s "https://your-server.example.com/api/v1/tasks/$TASK_ID" \
    -H "Authorization: Bearer $RASPICD_SECRET" | jq -r .status)
  echo "status: $STATUS"
  [[ "$STATUS" == "completed" || "$STATUS" == "failed" ]] && break
  sleep 5
done
```

**List all registered agents:**

```bash
curl -s https://your-server.example.com/api/v1/agents \
  -H "Authorization: Bearer $RASPICD_SECRET" | jq .
```

---

## Task signing

Beyond TLS, RasPiCD signs every task with an **Ed25519 private key** held by the server. Each agent holds the corresponding **public key** and verifies the signature before executing any script. A task with a missing or invalid signature is rejected immediately.

```
Server (private key) ── signs task ──▶ stored in server ── relayed to ──▶ Agent (public key verifies)
```

This means a task cannot be tampered with in transit or in storage without detection, even by someone who has obtained the CI/CD bearer token.

### Generate a keypair

Run the server once **without** `RASPICD_SIGNING_KEY` — it generates an ephemeral keypair and prints both values:

```
WARN RASPICD_SIGNING_KEY not set — using an ephemeral key that changes on every restart.
WARN To make signing permanent, add to your server config:  RASPICD_SIGNING_KEY=a3f4b2c1...
WARN Add to each agent's /etc/raspicd/agent.env:            RASPICD_VERIFY_KEY=9d72e1f0...
```

Copy the printed values into your config, then restart. Alternatively, generate a seed directly:

```bash
openssl rand -hex 32   # → use as RASPICD_SIGNING_KEY on the server
```

Then fetch the public key from the running server:

```bash
curl https://your-server.example.com/api/v1/pubkey
# {"public_key":"9d72e1f0..."}
```

### Configure the server

```bash
# docker-compose.yml / environment
RASPICD_SIGNING_KEY=a3f4b2c1...   # 64 hex chars (32-byte seed)
```

### Configure each agent

Add `RASPICD_VERIFY_KEY` to `/etc/raspicd/agent.env`:

```bash
RASPICD_VERIFY_KEY=9d72e1f0...    # 64 hex chars (32-byte public key)
```

Then restart the agent:

```bash
sudo systemctl restart raspicd-agent
```

If `RASPICD_VERIFY_KEY` is not set the agent logs a warning and executes tasks unsigned. **Always set it in production.**

---

## API Reference

Endpoints are split by which secret they require.

**Unauthenticated**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Server health check |
| `GET` | `/api/v1/pubkey` | Server's Ed25519 public key (for agent verification setup) |

**Agent secret** (`RASPICD_AGENT_SECRET`)

| Method | Path | Description |
|--------|------|-------------|
| `GET` (WS upgrade) | `/api/v1/agents/ws` | Persistent WebSocket connection — registration, task delivery, and result reporting all flow through here |

The agent opens this connection on startup and keeps it alive with WebSocket ping/pong frames. The server pushes task frames to the agent and receives result frames back over the same connection. On disconnect the agent is marked offline; pending tasks are queued and flushed on reconnect.

**CI secret** (`RASPICD_SECRET`)

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
