# RaspiDeploy

Deploy to Raspberry Pis from any CI/CD pipeline.

**How it works:** a lightweight server sits on the public internet. Each Pi runs an agent that polls the server for tasks. Your CI/CD pipeline pushes a task to the server — the next time the agent polls, it picks it up and executes it locally.

No inbound ports are needed on the Pi. The agent connects outbound only.

```
CI/CD pipeline  ──POST /api/v1/tasks──▶  Server (public)
                                              ▲
                 Agent (Pi) polls on startup, │then every 30s
```

---

## Requirements

| Component | Requirements |
|-----------|-------------|
| Server    | Any Linux host with Docker + Docker Compose |
| Agent     | Raspberry Pi running Linux, `git` and `bash` installed |

---

## 1. Deploy the Server

### Generate a secret

```bash
openssl rand -hex 32
```

Keep this value — you will need it on both the server and every agent.

### Run with Docker Compose

Create a `docker-compose.yml` on your server host:

```yaml
services:
  raspideploy-server:
    image: ghcr.io/your-org/raspideploy:latest
    container_name: raspideploy-server
    restart: unless-stopped
    environment:
      RASPIDEPLOY_SECRET: "${RASPIDEPLOY_SECRET}"
      RASPIDEPLOY_AGENT_SECRET: "${RASPIDEPLOY_AGENT_SECRET}"
    ports:
      - "8080:8080"
    volumes:
      - raspideploy-data:/data

volumes:
  raspideploy-data:
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
curl -fsSL -o /tmp/raspideploy-agent \
  "https://github.com/your-org/raspideploy/releases/download/${VERSION}/raspideploy-agent-linux-arm64"

# Pi 2 / 3 running a 32-bit OS
curl -fsSL -o /tmp/raspideploy-agent \
  "https://github.com/your-org/raspideploy/releases/download/${VERSION}/raspideploy-agent-linux-armv7"

sudo mv /tmp/raspideploy-agent /usr/local/bin/raspideploy-agent
sudo chmod +x /usr/local/bin/raspideploy-agent
```

### Configure the agent

Create the config directory and write the environment file:

```bash
sudo mkdir -p /etc/raspideploy
sudo tee /etc/raspideploy/agent.env > /dev/null <<EOF
RASPIDEPLOY_SERVER=https://your-server.example.com
RASPIDEPLOY_AGENT_ID=raspi-living-room
RASPIDEPLOY_AGENT_SECRET=<your-agent-secret>
RASPIDEPLOY_POLL_INTERVAL=30s
# RASPIDEPLOY_SCRIPTS_DIR=/etc/raspideploy/scripts
EOF

# Protect the file — it contains the secret
sudo chmod 600 /etc/raspideploy/agent.env
```

`RASPIDEPLOY_AGENT_ID` is the unique name you will use when targeting this Pi from CI/CD. Use something descriptive (`raspi-living-room`, `raspi-garage`, etc.).

### Agent environment variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `RASPIDEPLOY_SERVER` | Yes | — | Server base URL |
| `RASPIDEPLOY_AGENT_ID` | Yes | — | Unique name for this Pi |
| `RASPIDEPLOY_AGENT_SECRET` | Yes | — | Agent Bearer token secret |
| `RASPIDEPLOY_POLL_INTERVAL` | No | `30s` | How often to poll |
| `RASPIDEPLOY_SCRIPTS_DIR` | No | `/etc/raspideploy/scripts` | Directory of named scripts |
| `RASPIDEPLOY_DEBUG` | No | `false` | Verbose logging |

### Install as a systemd service

```bash
sudo cp deploy/agent.service /etc/systemd/system/raspideploy-agent.service
sudo systemctl daemon-reload
sudo systemctl enable raspideploy-agent
sudo systemctl start raspideploy-agent
```

**Check it is running:**

```bash
sudo systemctl status raspideploy-agent
sudo journalctl -u raspideploy-agent -f
```

The agent contacts the server immediately on startup, then continues to poll every 30 seconds. You should see a successful heartbeat log within seconds of starting the service.

### Set up named scripts

Named scripts are the recommended way to deploy (see [Task types](#task-types) below). Create the scripts directory and add executable shell scripts to it:

```bash
sudo mkdir -p /etc/raspideploy/scripts

sudo tee /etc/raspideploy/scripts/deploy-myapp.sh > /dev/null <<'EOF'
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

sudo chmod +x /etc/raspideploy/scripts/deploy-myapp.sh
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

The agent resolves `name` to `/etc/raspideploy/scripts/deploy-myapp.sh` and validates it before executing:
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

All endpoints except `/health` require `Authorization: Bearer <secret>`.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Server health check |
| `GET` | `/api/v1/agents` | List registered agents |
| `POST` | `/api/v1/agents/heartbeat` | Agent registration/keep-alive |
| `GET` | `/api/v1/agents/{id}/tasks` | Pending tasks for one agent |
| `POST` | `/api/v1/tasks` | Create a task |
| `GET` | `/api/v1/tasks` | List tasks (`?agent_id=`, `?status=`) |
| `GET` | `/api/v1/tasks/{id}` | Get a single task |
| `POST` | `/api/v1/tasks/{id}/result` | Agent reports task progress/completion |

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
git clone https://github.com/you/raspideploy.git
cd raspideploy
go mod download

make build              # server + agent for current platform
make build-agent-arm64  # agent for Pi 3/4/5 (64-bit)
make build-agent-armv7  # agent for Pi 2/3 (32-bit)
make docker-build       # build server Docker image
```

Requires Go 1.22+.
