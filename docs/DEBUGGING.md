# Debugging

## Runtime Freshness Check

Before deep debugging, confirm the session is running the latest daemon build:

```bash
./scripts/dev-status.sh
```

If output shows `STALE`, restart sidebar daemon for the target session:

```bash
TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='edge-test' ./scripts/toggle_sidebar.sh
TABBY_SKIP_BUILD=1 TABBY_SESSION_TARGET='edge-test' ./scripts/toggle_sidebar.sh
```

This avoids chasing issues from old daemon binaries after a rebuild.

## Input Logging

Tabby includes optional input logging to help diagnose issues with:
- Clicks not being received or processed
- Sidebar becoming unresponsive
- Connection drops between renderer and daemon

### Enable/Disable

```bash
# Enable input logging
tmux set-option -g @tabby_input_log on

# Disable input logging (default)
tmux set-option -g @tabby_input_log off
```

The setting is cached for 10 seconds, so changes take effect within that window.

### Log Files

When enabled, logs are written to:

| File | Description |
|------|-------------|
| `/tmp/tabby-daemon-$0-input.log` | Daemon-side input events |
| `/tmp/sidebar-renderer-@*-input.log` | Per-window renderer events |

### Log Events

**Daemon logs (`tabby-daemon-*-input.log`):**

| Event | Description |
|-------|-------------|
| `INPUT client=X type=Y button=Z x=N y=N action=A target=T` | Every input received |
| `INPUT_SLOW client=X type=Y elapsed=Nms` | Processing took >50ms |
| `CLICK_MISS client=X x=N y=N button=Z` | Click didn't hit any region |
| `HEALTH clients=N` | Connection count (every 5s) |

**Renderer logs (`sidebar-renderer-*-input.log`):**

| Event | Description |
|-------|-------------|
| `SEND button=X x=N y=N action=A target=T connected=bool` | Click sent to daemon |
| `SEND_FAILED not connected` | Click attempted while disconnected |
| `CONNECTED client=X` | Renderer connected to daemon |
| `DISCONNECTED` | Renderer lost connection |

### Real-time Monitoring

```bash
# Watch all input logs
tail -f /tmp/tabby-daemon-*-input.log /tmp/sidebar-renderer-*-input.log

# Watch daemon only
tail -f /tmp/tabby-daemon-*-input.log

# Watch specific window renderer
tail -f /tmp/sidebar-renderer-@9-input.log
```

### Diagnosing Common Issues

**Clicks not working:**
1. Enable logging: `tmux set-option -g @tabby_input_log on`
2. Click in sidebar
3. Check renderer log for `SEND` - if missing, renderer didn't detect click
4. Check daemon log for `INPUT` - if missing, message didn't reach daemon
5. Check for `CLICK_MISS` - click may be hitting empty area
6. Check for `SEND_FAILED` - renderer may be disconnected

**Sidebar unresponsive:**
1. Check `HEALTH clients=N` - should show expected client count
2. Look for `INPUT_SLOW` - indicates processing delays
3. Look for gaps in health checks - daemon may have crashed
4. Check for `DISCONNECTED` events without subsequent `CONNECTED`

**Intermittent issues:**
1. Keep logs enabled and let it run
2. When issue occurs, note the timestamp
3. Review logs around that time for anomalies

## Other Log Files

| File | Description |
|------|-------------|
| `/tmp/tabby-daemon-$0-crash.log` | Panic stack traces |
| `/tmp/tabby-daemon-$0-events.log` | Lifecycle events (spawn, connect, cleanup, restart requests) |
| `/tmp/sidebar-renderer-@*-crash.log` | Per-window crash logs |

## Debug Mode

For verbose debug output (not recommended for normal use):

```bash
# Start daemon with debug flag
tabby-daemon -session $0 -debug

# Start renderer with debug flag
sidebar-renderer -session $0 -window @9 -debug
```

Debug logs go to:
- Daemon: stderr
- Renderer: `/tmp/sidebar-renderer-@*-debug.log`
