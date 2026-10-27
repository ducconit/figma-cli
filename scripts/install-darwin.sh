#!/usr/bin/env bash
set -e

# ==============================================================================
# figma-cli (fm) Universal Installer for macOS (Apple Silicon / Darwin)
# Usage:
#   IDE=antigravity curl -fsSL https://raw.githubusercontent.com/ducconit/figma-cli/main/scripts/install-darwin.sh | bash
#   IDE=all curl -fsSL https://raw.githubusercontent.com/ducconit/figma-cli/main/scripts/install-darwin.sh | bash
# ==============================================================================

REPO="ducconit/figma-cli"
DEFAULT_SKILL_PATH="${HOME}/.agents/skills"
TARGET_PATH="${SKILL_PATH:-$DEFAULT_SKILL_PATH}"
SKILL_DIR="${TARGET_PATH}/figma-cli"
SCRIPTS_DIR="${SKILL_DIR}/scripts"

echo "==> Installing figma-cli (fm) for macOS..."

# 1. Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
    arm64|aarch64)
        ASSET_CANDIDATES=("fm-darwin-arm64" "fm-macos-arm64")
        ;;
    x86_64|amd64)
        ASSET_CANDIDATES=("fm-macos-amd64" "fm-darwin-amd64" "fm-macos-arm64")
        ;;
    *)
        ASSET_CANDIDATES=("fm-darwin-arm64" "fm-macos-arm64")
        ;;
esac

# 2. Download single native binary for macOS
mkdir -p "${SCRIPTS_DIR}"
TMP_BIN=$(mktemp)
DOWNLOAD_SUCCESS=0

for ASSET_NAME in "${ASSET_CANDIDATES[@]}"; do
    DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${ASSET_NAME}"
    if curl -fsSL "${DOWNLOAD_URL}" -o "${TMP_BIN}"; then
        echo "==> Downloaded ${ASSET_NAME} successfully."
        DOWNLOAD_SUCCESS=1
        break
    fi
done

if [ $DOWNLOAD_SUCCESS -ne 1 ]; then
    echo "Error: Failed to download release binary for macOS from GitHub." >&2
    rm -f "${TMP_BIN}"
    exit 1
fi

chmod +x "${TMP_BIN}"
mv -f "${TMP_BIN}" "${SCRIPTS_DIR}/fm"

# 3. Parse IDE environment variable
TARGET_IDE="${IDE:-all}"
IDE_FLAGS=""

IFS=', ' read -r -a IDE_LIST <<< "$TARGET_IDE"
for item in "${IDE_LIST[@]}"; do
    case "$(echo "$item" | tr '[:upper:]' '[:lower:]')" in
        antigravity|agy|gemini)
            IDE_FLAGS="$IDE_FLAGS --antigravity"
            ;;
        claude)
            IDE_FLAGS="$IDE_FLAGS --claude"
            ;;
        codex)
            IDE_FLAGS="$IDE_FLAGS --codex"
            ;;
        cursor)
            IDE_FLAGS="$IDE_FLAGS --cursor"
            ;;
        windsurf)
            IDE_FLAGS="$IDE_FLAGS --windsurf"
            ;;
        copilot)
            IDE_FLAGS="$IDE_FLAGS --copilot"
            ;;
        opencode)
            IDE_FLAGS="$IDE_FLAGS --opencode"
            ;;
        cline)
            IDE_FLAGS="$IDE_FLAGS --cline"
            ;;
        all)
            IDE_FLAGS="$IDE_FLAGS --all"
            ;;
        *)
            echo "Notice: Unknown IDE '$item', defaulting to --all"
            IDE_FLAGS="$IDE_FLAGS --all"
            ;;
    esac
done

# 4. Run `fm install` to scaffold SKILL.md, references, and create IDE symlinks
echo "==> Configuring Skill and AI Agent symlinks (IDE: ${TARGET_IDE})..."
"${SCRIPTS_DIR}/fm" install --path "${TARGET_PATH}" --os darwin ${IDE_FLAGS}

# 5. Optional token setup if TOKEN or FIGMA_API_KEY is provided
TOKEN_VAL="${TOKEN:-$FIGMA_API_KEY}"
if [ -n "$TOKEN_VAL" ]; then
    echo "==> Configuring Figma Personal Access Token..."
    "${SCRIPTS_DIR}/fm" set-token "$TOKEN_VAL"
fi

echo ""
echo "🎉 figma-cli (fm) installed successfully for macOS!"
echo "   Binary path : ${SCRIPTS_DIR}/fm"
echo "   Skill path  : ${SKILL_DIR}"
if [ -z "$TOKEN_VAL" ]; then
    echo "👉 Remember to set your Figma token:"
    echo "   ${SCRIPTS_DIR}/fm set-token figd_YOUR_PERSONAL_ACCESS_TOKEN"
fi
