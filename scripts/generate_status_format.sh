#!/usr/bin/env bash
CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && cd .. && pwd)"
CONFIG_FILE="$CURRENT_DIR/config.yaml"

SD_BG=$(grep -A4 'name: "StudioDome"' "$CONFIG_FILE" | grep 'bg:' | awk '{print $2}' | tr -d '"' || echo "#e74c3c")
SD_FG=$(grep -A4 'name: "StudioDome"' "$CONFIG_FILE" | grep 'fg:' | awk '{print $2}' | tr -d '"' || echo "#ffffff")
SD_ACTIVE_BG=$(grep -A5 'name: "StudioDome"' "$CONFIG_FILE" | grep 'active_bg:' | awk '{print $2}' | tr -d '"' || echo "#c0392b")
SD_ACTIVE_FG=$(grep -A5 'name: "StudioDome"' "$CONFIG_FILE" | grep 'active_fg:' | awk '{print $2}' | tr -d '"' || echo "#ffffff")

GP_BG=$(grep -A4 'name: "Gunpowder"' "$CONFIG_FILE" | grep 'bg:' | awk '{print $2}' | tr -d '"' || echo "#7f8c8d")
GP_FG=$(grep -A4 'name: "Gunpowder"' "$CONFIG_FILE" | grep 'fg:' | awk '{print $2}' | tr -d '"' || echo "#ecf0f1")
GP_ACTIVE_BG=$(grep -A5 'name: "Gunpowder"' "$CONFIG_FILE" | grep 'active_bg:' | awk '{print $2}' | tr -d '"' || echo "#34495e")
GP_ACTIVE_FG=$(grep -A5 'name: "Gunpowder"' "$CONFIG_FILE" | grep 'active_fg:' | awk '{print $2}' | tr -d '"' || echo "#ffffff")

DEFAULT_BG=$(grep -A4 'name: "Default"' "$CONFIG_FILE" | grep 'bg:' | awk '{print $2}' | tr -d '"' || echo "#3498db")
DEFAULT_FG=$(grep -A4 'name: "Default"' "$CONFIG_FILE" | grep 'fg:' | awk '{print $2}' | tr -d '"' || echo "#ecf0f1")
DEFAULT_ACTIVE_BG=$(grep -A5 'name: "Default"' "$CONFIG_FILE" | grep 'active_bg:' | awk '{print $2}' | tr -d '"' || echo "#2980b9")
DEFAULT_ACTIVE_FG=$(grep -A5 'name: "Default"' "$CONFIG_FILE" | grep 'active_fg:' | awk '{print $2}' | tr -d '"' || echo "#ffffff")

cat << EOF
#[align=left]#{W:#[range=window|#{window_index}]#{?#{m:SD\\\\|*,#W},#{?#{window_active},#[fg=$SD_ACTIVE_FG bg=$SD_ACTIVE_BG bold],#[fg=$SD_FG bg=$SD_BG]} #{window_index}:#W ,#{?#{m:GP\\\\|*,#W},#{?#{window_active},#[fg=$GP_ACTIVE_FG bg=$GP_ACTIVE_BG bold],#[fg=$GP_FG bg=$GP_BG]} 🔫 #{window_index}:#W ,#{?#{window_active},#[fg=$DEFAULT_ACTIVE_FG bg=$DEFAULT_ACTIVE_BG bold],#[fg=$DEFAULT_FG bg=$DEFAULT_BG]} #{window_index}:#W }}#[norange default] }
EOF
