#!/usr/bin/env bash
# RasPiCD Server installer
# Usage: curl -fsSL https://raw.githubusercontent.com/simonoche/raspi-cd/main/scripts/install-server.sh | sudo bash
set -euo pipefail

REPO="simonoche/raspi-cd"
INSTALL_BIN="/usr/local/bin/raspicd-server"
CONF="/etc/raspicd/server.env"
DATA_DIR="/var/lib/raspicd"
SERVICE_FILE="/etc/systemd/system/raspicd-server.service"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BOLD='\033[1m'; RESET='\033[0m'

die()  { echo -e "${RED}Error: $*${RESET}" >&2; exit 1; }
info() { echo -e "${GREEN}▶${RESET} $*"; }
warn() { echo -e "${YELLOW}Warning: $*${RESET}"; }

# ── Preflight ─────────────────────────────────────────────────────────────────

[ "$(uname -s)" = "Linux" ] || die "This installer supports Linux only."
[ "$(id -u)" = "0" ] || die "Please run as root (sudo bash install-server.sh)"
command -v openssl >/dev/null 2>&1 || die "openssl is required but not installed."

# ── Architecture ──────────────────────────────────────────────────────────────

MACHINE=$(uname -m)
case "$MACHINE" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)             die "Unsupported architecture: $MACHINE (server supports amd64 and arm64)" ;;
esac
info "Detected architecture: ${MACHINE} → ${ARCH}"

# ── Latest release ────────────────────────────────────────────────────────────

info "Fetching latest release from GitHub…"
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
[ -n "$LATEST" ] || die "Could not determine latest release. Check your internet connection."
info "Latest release: ${LATEST}"

# ── Download binary ───────────────────────────────────────────────────────────

BINARY_URL="https://github.com/${REPO}/releases/download/${LATEST}/raspicd-server-linux-${ARCH}"
info "Downloading ${BINARY_URL}…"
curl -fsSL "$BINARY_URL" -o /tmp/raspicd-server
chmod +x /tmp/raspicd-server
mv /tmp/raspicd-server "$INSTALL_BIN"
info "Installed binary → ${INSTALL_BIN}"

# ── System user ───────────────────────────────────────────────────────────────

if ! id -u raspicd >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin raspicd
    info "Created system user: raspicd"
fi

# ── Directories ───────────────────────────────────────────────────────────────

mkdir -p /etc/raspicd "$DATA_DIR"
chmod 750 /etc/raspicd
chmod 750 "$DATA_DIR"
chown raspicd:raspicd "$DATA_DIR"

# ── Generate secrets & configuration ─────────────────────────────────────────

if [ ! -f "$CONF" ]; then
    echo ""
    echo -e "${BOLD}┌──────────────────────────────────────────────────────┐${RESET}"
    echo -e "${BOLD}│        RasPiCD Server — Generating Secrets           │${RESET}"
    echo -e "${BOLD}└──────────────────────────────────────────────────────┘${RESET}"
    echo ""

    CI_SECRET=$(openssl rand -hex 32)
    AGENT_SECRET=$(openssl rand -hex 32)
    SIGNING_KEY=$(openssl rand -hex 32)
    VERIFY_KEY=$("$INSTALL_BIN" --signing-key="$SIGNING_KEY" --print-pubkey 2>/dev/null || true)

    {
        echo "RASPICD_SECRET=${CI_SECRET}"
        echo "RASPICD_AGENT_SECRET=${AGENT_SECRET}"
        echo "RASPICD_SIGNING_KEY=${SIGNING_KEY}"
        echo "RASPICD_DATA_FILE=${DATA_DIR}/store.json"
        echo "# RASPICD_BIND=:8080"
        echo "# RASPICD_AGENT_TIMEOUT=90s"
        echo "# RASPICD_DEBUG=false"
    } > "$CONF"
    chmod 600 "$CONF"
    chown raspicd:raspicd "$CONF"
    info "Configuration written → ${CONF}"
else
    warn "Config file already exists — skipping secret generation: ${CONF}"
    CI_SECRET=$(grep "^RASPICD_SECRET=" "$CONF" | cut -d= -f2 || echo "(see ${CONF})")
    AGENT_SECRET=$(grep "^RASPICD_AGENT_SECRET=" "$CONF" | cut -d= -f2 || echo "(see ${CONF})")
    SIGNING_KEY=$(grep "^RASPICD_SIGNING_KEY=" "$CONF" | cut -d= -f2 || echo "")
    if [ -n "$SIGNING_KEY" ]; then
        VERIFY_KEY=$("$INSTALL_BIN" --signing-key="$SIGNING_KEY" --print-pubkey 2>/dev/null \
            || echo "(run: curl http://localhost:8080/api/v1/pubkey)")
    else
        VERIFY_KEY="(run: curl http://localhost:8080/api/v1/pubkey)"
    fi
fi

# ── Systemd service ───────────────────────────────────────────────────────────

if command -v systemctl >/dev/null 2>&1; then
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=RasPiCD Server
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
    systemctl enable raspicd-server
    systemctl start raspicd-server
    info "Service enabled and started: raspicd-server"
else
    warn "systemd not found — start manually: ${INSTALL_BIN}"
fi

# ── Done — print keys ─────────────────────────────────────────────────────────

echo ""
echo -e "${BOLD}┌──────────────────────────────────────────────────────────────────┐${RESET}"
echo -e "${BOLD}│                   RasPiCD Server — Keys                         │${RESET}"
echo -e "${BOLD}│           Save these — you will need them for your agents        │${RESET}"
echo -e "${BOLD}└──────────────────────────────────────────────────────────────────┘${RESET}"
echo ""
echo -e "  ${BOLD}RASPICD_SECRET${RESET}       (for CI/CD pipelines)"
echo    "  ${CI_SECRET}"
echo ""
echo -e "  ${BOLD}RASPICD_AGENT_SECRET${RESET} (for each agent's agent.env)"
echo    "  ${AGENT_SECRET}"
echo ""
echo -e "  ${BOLD}RASPICD_VERIFY_KEY${RESET}   (add to each agent's agent.env)"
echo    "  ${VERIFY_KEY}"
echo ""
echo "──────────────────────────────────────────────────────────────────"
echo ""
echo -e "${GREEN}✓ RasPiCD Server ${LATEST} installed successfully.${RESET}"
echo ""
if command -v systemctl >/dev/null 2>&1; then
    echo "  Status  : sudo systemctl status raspicd-server"
    echo "  Logs    : sudo journalctl -u raspicd-server -f"
    echo "  Health  : curl http://localhost:8080/health"
fi
echo "  Config  : ${CONF}"
echo ""
