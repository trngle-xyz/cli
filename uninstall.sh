#!/bin/sh
# Uninstall trngle CLI
# Usage: curl -fsSL https://cli.trngle.xyz/uninstall.sh | sh
set -e

BINARY="trngle"
LOCATIONS="${HOME}/.local/bin/${BINARY} /usr/local/bin/${BINARY}"
CONFIG_DIR="${HOME}/.trngle"

found=0
for path in $LOCATIONS; do
  if [ -f "$path" ]; then
    rm -f "$path"
    echo "Removed $path"
    found=1
  fi
done

if [ "$found" = "0" ]; then
  echo "trngle binary not found in standard locations."
  echo "If you installed it elsewhere, remove it manually."
fi

if [ -d "$CONFIG_DIR" ]; then
  printf "Remove config and history at %s? [y/N] " "$CONFIG_DIR"
  read -r answer
  case "$answer" in
    [yY]|[yY][eE][sS])
      rm -rf "$CONFIG_DIR"
      echo "Removed $CONFIG_DIR"
      ;;
    *)
      echo "Kept $CONFIG_DIR"
      ;;
  esac
fi

echo ""
echo "trngle uninstalled."
