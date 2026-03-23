#!/bin/sh
# Install trngle CLI — https://trngle.xyz
# Usage: curl -fsSL https://raw.githubusercontent.com/trngle-xyz/cli/main/install.sh | sh
set -e

REPO="trngle-xyz/cli"
BINARY="trngle"
INSTALL_DIR="${HOME}/.local/bin"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux)  OS="linux" ;;
  darwin) OS="darwin" ;;
  *)      echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)             echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest release tag
echo "Finding latest release..."
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
if [ -z "$LATEST" ]; then
  echo "Error: could not find latest release. Check https://github.com/${REPO}/releases"
  exit 1
fi
echo "Latest version: ${LATEST}"

# Download
FILENAME="${BINARY}-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/${LATEST}/${FILENAME}"
echo "Downloading ${FILENAME}..."
TMPDIR=$(mktemp -d)
curl -fsSL -o "${TMPDIR}/${BINARY}" "$URL"
chmod +x "${TMPDIR}/${BINARY}"

# Install to ~/.local/bin (no sudo needed)
mkdir -p "$INSTALL_DIR"
mv "${TMPDIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
rm -rf "$TMPDIR"

# Check if ~/.local/bin is in PATH
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo ""
    echo "NOTE: ${INSTALL_DIR} is not in your PATH."
    echo "Add it by running:"
    echo ""
    SHELL_NAME="$(basename "${SHELL:-/bin/sh}")"
    case "$SHELL_NAME" in
      zsh)  echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.zshrc && source ~/.zshrc" ;;
      fish) echo "  fish_add_path ${INSTALL_DIR}" ;;
      *)    echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc && source ~/.bashrc" ;;
    esac
    ;;
esac

echo ""
echo "trngle ${LATEST} installed to ${INSTALL_DIR}/${BINARY}"
echo ""
echo "Get started:"
echo "  trngle"
echo ""
