#!/bin/bash

# Exit on error
set -e

PROJECT_DIR=$(pwd)
EXTENSION_ID="linak-desk@asa.github.io"
EXTENSION_DEST="$HOME/.local/share/gnome-shell/extensions/$EXTENSION_ID"
SYSTEMD_DEST="$HOME/.config/systemd/user"
BIN_DEST="$HOME/.local/bin"

echo "Building Linak Desk Control..."
go build -o linak-ctl main.go

echo "Installing binary to $BIN_DEST..."
mkdir -p "$BIN_DEST"
cp linak-ctl "$BIN_DEST/linak-ctl"
chmod +x "$BIN_DEST/linak-ctl"

echo "Installing GNOME Shell Extension..."
mkdir -p "$EXTENSION_DEST"
cp -r extension/* "$EXTENSION_DEST/"

echo "Installing Systemd User Service..."
mkdir -p "$SYSTEMD_DEST"
# Update service file to point to the user bin directory
sed "s|ExecStart=.*|ExecStart=$BIN_DEST/linak-ctl|" linak-desk.service > "$SYSTEMD_DEST/linak-desk.service"

echo "Reloading services..."
systemctl --user daemon-reload
systemctl --user enable linak-desk.service
systemctl --user restart linak-desk.service

echo "-------------------------------------------------------"
echo "Installation complete!"
echo "1. The desk control daemon is now running in the background."
echo "2. PLEASE RESTART GNOME SHELL (logout and login) to see the new menu."
echo "3. After logging back in, enable it with:"
echo "   gnome-extensions enable $EXTENSION_ID"
echo "-------------------------------------------------------"
