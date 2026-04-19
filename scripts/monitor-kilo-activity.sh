#!/bin/bash
# Monitor Kilo activity and set Tabby indicators
# Run this in background when using Kilo

set -u

TABBY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INDICATOR="$TABBY_DIR/bin/tabby hook set-indicator"
LOG_FILE="/tmp/kilo-activity-monitor.log"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') $*" >> "$LOG_FILE"
}

set_indicator() {
    if command -v "$(echo $INDICATOR | awk '{print $1}')" &>/dev/null; then
        $INDICATOR "$1" "$2" 2>/dev/null || true
        log "Set indicator: $1=$2"
    fi
}

# Clear indicators on exit
cleanup() {
    set_indicator busy 0
    set_indicator bell 0
    log "Cleanup: cleared all indicators"
    exit 0
}
trap cleanup EXIT INT TERM

log "Starting Kilo activity monitor"

# Monitor for Kilo process and activity
LAST_KILO_PID=""
BUSY_STATE=0

while true; do
    # Check if Kilo is running
    KILO_PID=$(pgrep -f "kilo" | head -1)
    
    if [[ -n "$KILO_PID" ]]; then
        # Kilo is running
        if [[ "$KILO_PID" != "$LAST_KILO_PID" ]]; then
            log "Detected new Kilo process: $KILO_PID"
            LAST_KILO_PID="$KILO_PID"
        fi
        
        # Check CPU usage to detect if Kilo is actively working
        CPU_USAGE=$(ps -p "$KILO_PID" -o %cpu= 2>/dev/null | tr -d ' ' || echo "0")
        
        # If CPU usage is above threshold, assume it's working
        if (( $(echo "$CPU_USAGE > 5" | bc -l 2>/dev/null || echo 0) )); then
            if [[ "$BUSY_STATE" != "1" ]]; then
                set_indicator busy 1
                BUSY_STATE=1
                log "Kilo busy (CPU: ${CPU_USAGE}%)"
            fi
        else
            if [[ "$BUSY_STATE" == "1" ]]; then
                set_indicator busy 0
                set_indicator bell 1
                BUSY_STATE=0
                log "Kilo idle (CPU: ${CPU_USAGE}%)"
                
                # Clear bell after a delay
                (sleep 3; set_indicator bell 0) &
            fi
        fi
    else
        # Kilo not running
        if [[ -n "$LAST_KILO_PID" ]]; then
            log "Kilo process ended"
            set_indicator busy 0
            set_indicator bell 0
            LAST_KILO_PID=""
            BUSY_STATE=0
        fi
    fi
    
    sleep 1
done