#!/usr/bin/env bash
# RasPiCD Agent installer
# Usage: curl -fsSL https://raw.githubusercontent.com/simonoche/raspi-cd/main/scripts/install-agent.sh | sudo bash
set -euo pipefail

REPO="simonoche/raspi-cd"
INSTALL_BIN="/usr/local/bin/raspicd-agent"
CONF="/etc/raspicd/agent.env"
SCRIPTS_DIR="/etc/raspicd/scripts"
SERVICE_FILE="/etc/systemd/system/raspicd-agent.service"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; RESET='\033[0m'

die()  { echo -e "${RED}Error: $*${RESET}" >&2; exit 1; }
info() { echo -e "${GREEN}▶${RESET} $*"; }
warn() { echo -e "${YELLOW}Warning: $*${RESET}"; }

# ── Preflight ─────────────────────────────────────────────────────────────────

[ "$(uname -s)" = "Linux" ] || die "This installer supports Linux only."
[ "$(id -u)" = "0" ] || die "Please run as root (sudo bash install-agent.sh)"

# ── Architecture ──────────────────────────────────────────────────────────────

MACHINE=$(uname -m)
case "$MACHINE" in
  x86_64)         ARCH="amd64" ;;
  aarch64|arm64)  ARCH="arm64" ;;
  armv7l|armhf)   ARCH="armv7" ;;
  *)              die "Unsupported architecture: $MACHINE" ;;
esac
info "Detected architecture: ${MACHINE} → ${ARCH}"

# ── Latest release ────────────────────────────────────────────────────────────

info "Fetching latest release from GitHub…"
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
[ -n "$LATEST" ] || die "Could not determine latest release. Check your internet connection."
info "Latest release: ${LATEST}"

# ── Download binary ───────────────────────────────────────────────────────────

BINARY_URL="https://github.com/${REPO}/releases/download/${LATEST}/raspicd-agent-linux-${ARCH}"
info "Downloading ${BINARY_URL}…"
curl -fsSL "$BINARY_URL" -o /tmp/raspicd-agent
chmod +x /tmp/raspicd-agent
mv /tmp/raspicd-agent "$INSTALL_BIN"
info "Installed binary → ${INSTALL_BIN}"

# ── System user ───────────────────────────────────────────────────────────────

if ! id -u raspicd >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin raspicd
    info "Created system user: raspicd"
fi

# ── Directories ───────────────────────────────────────────────────────────────

mkdir -p "$SCRIPTS_DIR"
chmod 750 /etc/raspicd "$SCRIPTS_DIR"

# ── Configuration ─────────────────────────────────────────────────────────────

if [ ! -f "$CONF" ]; then
    echo ""
    echo -e "${BOLD}┌──────────────────────────────────────────────────────┐${RESET}"
    echo -e "${BOLD}│            RasPiCD Agent — Configuration             │${RESET}"
    echo -e "${BOLD}└──────────────────────────────────────────────────────┘${RESET}"
    echo ""

    while [ -z "${SERVER_URL:-}" ]; do
        printf "  Server URL (e.g. https://raspicd.example.com): "
        read -r SERVER_URL
    done

    printf "  Agent ID [%s]: " "$(hostname)"
    read -r AGENT_ID
    AGENT_ID="${AGENT_ID:-$(hostname)}"

    while [ -z "${AGENT_SECRET:-}" ]; do
        printf "  Agent secret (RASPICD_AGENT_SECRET from the server): "
        read -r AGENT_SECRET
    done

    printf "  Verify key (RASPICD_VERIFY_KEY from the server, leave empty to skip): "
    read -r VERIFY_KEY

    {
        echo "RASPICD_SERVER=${SERVER_URL}"
        echo "RASPICD_AGENT_ID=${AGENT_ID}"
        echo "RASPICD_AGENT_SECRET=${AGENT_SECRET}"
        [ -n "${VERIFY_KEY:-}" ] && echo "RASPICD_VERIFY_KEY=${VERIFY_KEY}"
        echo "# RASPICD_SCRIPTS_DIR=/etc/raspicd/scripts"
        echo "# RASPICD_POLL_INTERVAL=60s"
        echo "# RASPICD_DEBUG=false"
    } > "$CONF"
    chmod 600 "$CONF"
    info "Configuration written → ${CONF}"
else
    warn "Config file already exists — skipping configuration: ${CONF}"
fi

chown -R raspicd:raspicd /etc/raspicd

# ── Systemd service ───────────────────────────────────────────────────────────

if command -v systemctl >/dev/null 2>&1; then
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=RasPiCD Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=raspicd
EnvironmentFile=${CONF}
ExecStart=${INSTALL_BIN}
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable raspicd-agent
    systemctl start raspicd-agent
    info "Service enabled and started: raspicd-agent"
else
    warn "systemd not found — skipping service installation. Start manually: ${INSTALL_BIN}"
fi

# ── Done ──────────────────────────────────────────────────────────────────────

echo ""
echo -e "${GREEN}✓ RasPiCD Agent ${LATEST} installed successfully.${RESET}"
echo ""
if command -v systemctl >/dev/null 2>&1; then
    echo "  Status  : sudo systemctl status raspicd-agent"
    echo "  Logs    : sudo journalctl -u raspicd-agent -f"
fi
echo "  Scripts : ${SCRIPTS_DIR}/"
echo "  Config  : ${CONF}"
echo ""
