# RaspiDeploy

Deploy to Raspberry Pis from any CI/CD pipeline.

**How it works:** a lightweight server sits on the public internet. Each Pi runs an agent that polls the server for tasks. Your CI/CD pipeline pushes a task to the server — the next time the agent polls, it picks it up and executes it locally (git clone/pull + shell commands).

No inbound ports are needed on the Pi. The agent connects outbound only.

```
CI/CD pipeline  ──POST /api/v1/tasks──▶  Server (public)
                                              ▲
                              Agent (Pi) polls│every 30s
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
    ports:
      - "8080:8080"
    volumes:
      - raspideploy-data:/data

volumes:
  raspideploy-data:
```

Then start it:

```bash
export RASPIDEPLOY_SECRET=<your-secret>
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
| `RASPIDEPLOY_SECRET` | Yes | — | Shared auth secret |
| `RASPIDEPLOY_BIND` | No | `:8080` | Listen address |
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
RASPIDEPLOY_SECRET=<your-secret>
RASPIDEPLOY_POLL_INTERVAL=30s
EOF

# Protect the file — it contains the secret
sudo chmod 600 /etc/raspideploy/agent.env
```

`RASPIDEPLOY_AGENT_ID` is the unique name you will use when targeting this Pi from CI/CD. Use something descriptive (`raspi-living-room`, `raspi-garage`, etc.).

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

You should see a heartbeat log every 30 seconds.

---

## 3. Trigger a Deployment from CI/CD

### Task types

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

Add `RASPIDEPLOY_SECRET` and `RASPIDEPLOY_SERVER` as repository secrets, then add a deployment step:

```yaml
- name: Deploy to Raspberry Pi
  run: |
    curl -sf -X POST "$RASPIDEPLOY_SERVER/api/v1/tasks" \
      -H "Authorization: Bearer $RASPIDEPLOY_SECRET" \
      -H "Content-Type: application/json" \
      -d '{
        "type": "deploy",
        "agent_id": "raspi-living-room",
        "payload": {
          "repo_url":   "https://github.com/${{ github.repository }}.git",
          "ref":        "${{ github.ref_name }}",
          "target_dir": "/opt/myapp",
          "commands":   ["make install", "systemctl restart myapp"]
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
          "type": "deploy",
          "agent_id": "raspi-living-room",
          "payload": {
            "repo_url":   "'$CI_REPOSITORY_URL'",
            "ref":        "'$CI_COMMIT_REF_NAME'",
            "target_dir": "/opt/myapp",
            "commands":   ["make install", "systemctl restart myapp"]
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
    "type": "deploy",
    "agent_id": "raspi-living-room",
    "payload": {
      "repo_url":   "https://github.com/you/myapp.git",
      "ref":        "v1.2.3",
      "target_dir": "/opt/myapp",
      "commands":   ["make install", "systemctl restart myapp"]
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
