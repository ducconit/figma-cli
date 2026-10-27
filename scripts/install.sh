#!/usr/bin/env bash
set -e

# ==============================================================================
# figma-cli (fm) Unified 1-Command Installer (Linux & macOS)
# Usage:
#   IDE=antigravity curl -fsSL https://raw.githubusercontent.com/ducconit/figma-cli/main/scripts/install.sh | bash
#   IDE=all curl -fsSL https://raw.githubusercontent.com/ducconit/figma-cli/main/scripts/install.sh | bash
# ==============================================================================

OS="$(uname -s)"
case "$OS" in
    Linux)
        exec bash <(curl -fsSL https://raw.githubusercontent.com/ducconit/figma-cli/main/scripts/install-linux.sh)
        ;;
    Darwin)
        exec bash <(curl -fsSL https://raw.githubusercontent.com/ducconit/figma-cli/main/scripts/install-darwin.sh)
        ;;
    *)
        echo "Error: Unsupported operating system $OS. Please use scripts/install-windows.ps1 for Windows." >&2
        exit 1
        ;;
esac
