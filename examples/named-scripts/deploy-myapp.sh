#!/usr/bin/env bash
# deploy-myapp.sh — install this file at /etc/raspideploy/scripts/deploy-myapp.sh
# on each Raspberry Pi, then make it executable:
#
#   chmod +x /etc/raspideploy/scripts/deploy-myapp.sh
#
# Trigger it from CI/CD:
#
#   curl -X POST https://your-server/api/v1/tasks \
#     -H "Authorization: Bearer $RASPIDEPLOY_SECRET" \
#     -H "Content-Type: application/json" \
#     -d '{
#           "type":     "named_script",
#           "agent_id": "raspi-living-room",
#           "payload":  {
#             "name": "deploy-myapp",
#             "config": {
#               "ref":     "v1.2.3",
#               "env":     "production",
#               "restart": true
#             }
#           }
#         }'
#
# ---------------------------------------------------------------------------
# Environment variables injected by the agent:
#
#   RASPIDEPLOY_TASK_ID          — unique task ID (useful for logging)
#   RASPIDEPLOY_AGENT_ID         — name of this agent
#   RASPIDEPLOY_CONFIG           — full config as a JSON string
#
#   Per top-level config key (string / number / bool only):
#   RASPIDEPLOY_CONFIG_REF       — "v1.2.3"
#   RASPIDEPLOY_CONFIG_ENV       — "production"
#   RASPIDEPLOY_CONFIG_RESTART   — "true"
# ---------------------------------------------------------------------------

set -euo pipefail

APP_DIR="/opt/myapp"
REF="${RASPIDEPLOY_CONFIG_REF:-main}"
ENV="${RASPIDEPLOY_CONFIG_ENV:-production}"
RESTART="${RASPIDEPLOY_CONFIG_RESTART:-false}"

echo "[task: $RASPIDEPLOY_TASK_ID] deploying ref=$REF env=$ENV"

# Pull the requested ref.
if [ -d "$APP_DIR/.git" ]; then
    git -C "$APP_DIR" fetch --tags --prune origin
    git -C "$APP_DIR" checkout -f "$REF"
    git -C "$APP_DIR" pull --ff-only || true   # no-op for tags
else
    git clone --branch "$REF" https://github.com/your-org/myapp.git "$APP_DIR"
fi

# Build.
cd "$APP_DIR"
make build APP_ENV="$ENV"

# Optionally restart the service.
if [ "$RESTART" = "true" ]; then
    echo "restarting myapp.service ..."
    systemctl restart myapp
fi

echo "done."
