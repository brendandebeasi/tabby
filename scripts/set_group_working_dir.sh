#!/bin/bash
# Set working directory for a group in config.yaml
# Usage: set_group_working_dir.sh <group_name> <working_dir>

GROUP_NAME="$1"
WORKING_DIR="$2"

if [ -z "$GROUP_NAME" ] || [ -z "$WORKING_DIR" ]; then
    echo "Usage: set_group_working_dir.sh <group_name> <working_dir>"
    exit 1
fi

CONFIG_FILE="$HOME/.tmux/plugins/tmux-tabs/config.yaml"

if [ ! -f "$CONFIG_FILE" ]; then
    echo "Config file not found: $CONFIG_FILE"
    exit 1
fi

# Use Python for reliable YAML editing
python3 << EOF
import yaml
import sys

config_file = "$CONFIG_FILE"
group_name = "$GROUP_NAME"
working_dir = "$WORKING_DIR"

# Expand ~ to home directory for storage
if working_dir.startswith("~"):
    import os
    working_dir = working_dir  # Keep ~ in config for portability

try:
    with open(config_file, 'r') as f:
        config = yaml.safe_load(f)

    # Find and update the group
    found = False
    for group in config.get('groups', []):
        if group.get('name') == group_name:
            group['working_dir'] = working_dir
            found = True
            break

    if not found:
        print(f"Group '{group_name}' not found in config")
        sys.exit(1)

    with open(config_file, 'w') as f:
        yaml.dump(config, f, default_flow_style=False, sort_keys=False, allow_unicode=True)

    print(f"Set working_dir for '{group_name}' to '{working_dir}'")
except Exception as e:
    print(f"Error: {e}")
    sys.exit(1)
EOF

# Signal sidebars to refresh
for pid in $(tmux list-panes -a -F '#{pane_current_command}|#{pane_pid}' | grep '^sidebar|' | cut -d'|' -f2); do
    kill -USR1 "$pid" 2>/dev/null
done
