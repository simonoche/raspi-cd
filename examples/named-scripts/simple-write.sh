#!/usr/bin/env bash
# simple-write.sh — install this file at /etc/raspicd/scripts/simple-write.sh
# on each Raspberry Pi, then make it executable:
#
#   chmod +x /etc/raspicd/scripts/simple-write.sh
#
# Trigger it from CI/CD:
#
#   curl -X POST https://your-server/api/v1/tasks \
#     -H "Authorization: Bearer $RASPICD_SECRET" \
#     -H "Content-Type: application/json" \
#     -d '{
#           "agent_id": "raspi-living-room",
#           "script":   "simple-write",
#           "config": {
#             "ref":     "v1.2.3",
#             "env":     "production",
#             "restart": true
#           }
#         }'
#
# ---------------------------------------------------------------------------
# Environment variables injected by the agent:
#
#   RASPICD_TASK_ID          — unique task ID (useful for logging)
#   RASPICD_AGENT_ID         — name of this agent
#   RASPICD_CONFIG           — full config as a JSON string
#
#   Per top-level config key (string / number / bool only):
#   RASPICD_CONFIG_REF       — "v1.2.3"
#   RASPICD_CONFIG_ENV       — "production"
#   RASPICD_CONFIG_RESTART   — "true"
# ---------------------------------------------------------------------------

set -euo pipefail

APP_DIR="/home/simon/myapp"
REF="${RASPICD_CONFIG_REF:-main}"
ENV="${RASPICD_CONFIG_ENV:-production}"
RESTART="${RASPICD_CONFIG_RESTART:-false}"

echo "[task: $RASPICD_TASK_ID] deploying ref=$REF env=$ENV"
echo $REF > "$APP_DIR/config.txt"
echo "done."