[Home](Home.md) › Widgets

# Widgets

Widgets sit below the window list in the sidebar. Configure each one under
`widgets:` in `config.yaml`.

| Widget | Default | What it shows |
|---|---|---|
| `clock` | on | Local time, optionally the date |
| `pet` | on | A terminal pet with hunger and happiness state, optional LLM thought bubbles |
| `stats` | on | CPU, memory and battery |
| `git` | off | Branch, dirty state, ahead and behind counts, stash count for the active pane's directory |
| `session` | off | Session name, attached clients, window count |
| `claude` | off | Claude Code usage for today, week, month and total |
| `teamclaude` | off | Per-account quota from a teamclaude proxy |
| `kimi` | off | Kimi for Coding session and weekly quota |

## Shared options

Every widget takes the same layout controls:

```yaml
widgets:
  clock:
    enabled: true
    position: bottom        # top or bottom
    pin: true               # stay visible when the window list scrolls
    priority: 100           # lower renders closer to the bottom edge
    fg: "#888888"
    bg: ""
    divider: "─"
    divider_bottom: ""
    divider_fg: "#444444"
    margin_top: 1
    margin_bottom: 0
    padding_top: 0
    padding_bottom: 0
```

Rendered order, top to bottom:

```
[margin_top]
[divider]
[padding_top]
[content]
[padding_bottom]
[divider_bottom]
[margin_bottom]
```

With several pinned widgets, `priority` decides the stacking. Lower numbers sit
closer to the bottom edge, so priority 100 renders below priority 50.

Divider characters worth trying: `─` light, `━` heavy, `=` double, `-` ASCII,
`·` dots, or a space for pure spacing with no visible rule.

## Clock

```yaml
widgets:
  clock:
    enabled: true
    format: "15:04:05"
    show_date: true
    date_format: "Mon Jan 2"
```

Both formats use Go's reference time, `Mon Jan 2 15:04:05 MST 2006`.

| Format | Renders as |
|---|---|
| `15:04` | 14:30 |
| `15:04:05` | 14:30:45 |
| `3:04 PM` | 2:30 PM |
| `15:04 MST` | 14:30 PST |
| `Mon Jan 2` | Wed Jan 22 |
| `2006-01-02` | 2026-01-22 |
| `01/02/06` | 01/22/26 |

## Stats

```yaml
widgets:
  stats:
    enabled: true
    show_cpu: true
    show_memory: true
    show_battery: true
    style: nerd           # nerd, emoji, ascii, minimal
    bar_style: block      # block, braille, dots, ascii
    bar_width: 8
    cpu_fg: "#e0def4"
    memory_fg: "#e0def4"
    battery_fg: "#e0def4"
```

Pick `style: ascii` and `bar_style: ascii` if your font lacks Nerd Font glyphs.
`minimal` drops the icons and prints bare numbers.

## Git

Reads the active pane's working directory, so it follows you around.

```yaml
widgets:
  git:
    enabled: true
    position: bottom
    priority: 40
```

Shows the branch, whether the tree is dirty, ahead and behind counts against the
upstream, and the stash count.

## Session

```yaml
widgets:
  session:
    enabled: true
```

Session name, number of attached clients, window count. Useful when you keep
several sessions on one host.

## Pet

```yaml
widgets:
  pet:
    enabled: true
    name: "Tabs"
    style: emoji          # emoji, nerd, ascii
    rows: 3
    hunger_decay: 0.5
    poop_chance: 0.02
    debug_bar: false
```

The pet gets hungry over time, can be fed, and has an adventure mode. State
lives in `~/.local/state/tabby/pet.json`.

On an adventure the cat scrolls through a biome, hops over rocks and logs, and
sometimes climbs one and sits there a while before moving on. It hunts what it
finds, and a good catch occasionally comes home as a present, left on the
ground for you — click it to accept, or the cat takes it back after ten
minutes.

```yaml
widgets:
  pet:
    adventure_enabled: true
    adventure_chance: 100   # % of the time the cat acts on the urge to wander
    adventure_blood: false  # splatter on a successful pounce
```

The cat only leaves when there is nothing going on at home: no yarn or food
out, no mouse to chase, no poop to scoop, no unanswered question, and it has
been genuinely idle for a few seconds first. `adventure_chance` then gates how
often it actually goes. This key used to be documented but never read; leaving
it unset keeps the old behaviour.

The cat's permanent record — time spent in each state, adventures and catches,
care streaks, fastest yarn catch, favourite biome — accumulates in the same
`pet.json` under `lifetime_stats`. A `pet.json` from an older version loads
fine; the counters simply start at zero, and the cat's birthday is backdated to
the first time the new version sees it.

It can also think out loud, using an LLM:

```yaml
widgets:
  pet:
    thoughts: true
    thought_interval: 300      # seconds between new thoughts
    thought_speed: 40          # typing speed
```

Credentials come from `ANTHROPIC_API_KEY` and `ANTHROPIC_BASE_URL` by default.
Keeping them in the environment rather than in `config.yaml` avoids committing a
key by accident. The explicit `llm_provider`, `llm_base_url` and `llm_api_key`
keys exist if you need a different endpoint. Generated thoughts are cached in
`~/.local/state/tabby/thought_buffer.txt`.

Override individual sprites with an `icons:` map if the defaults do not suit
your font.

## Claude usage

```yaml
widgets:
  claude:
    enabled: true
```

Reads Claude Code's local sqlite history and shows spend for today, this week,
this month, and all time. Nothing leaves your machine.

## TeamClaude quotas

[teamclaude](https://github.com/KarpelesLab/teamclaude) is a multi-account
Claude proxy that rotates accounts by remaining quota. The widget polls its
`GET /teamclaude/status` endpoint and draws, per account, the session (5 hour)
and weekly (7 day) quota left as bars with the percentage and reset countdown
inside, for example `87% 4h`, coloured green, yellow or red by headroom.

```yaml
widgets:
  teamclaude:
    enabled: true
    url: "http://your-gateway:8081"
    show_session: true
    show_weekly: true
    update_interval: 60
    position: bottom
    priority: 60
```

The proxy load-balances, so more than one account can be serving at once. Each
active account is highlighted green with a live marker: `active(N/M)` for N
in-flight requests against a concurrency cap of M, `active(N)` when the cap is
unknown, or a bare `active` for an account used within the last 15 minutes. The
primary account is marked `▸`. On a narrow sidebar the marker is dropped. Older
proxies that do not report `activeRequests` and `maxConcurrency` fall back to
the recency signal alone.

Supply the key through the environment rather than the config file:

```bash
export TABBY_TEAMCLAUDE_API_KEY=tc-...
```

teamclaude skips auth for localhost, so no key is needed when the proxy and
Tabby run on the same host.

Fetches happen off the render path on the `update_interval` cadence. An
unreachable proxy shows a one-line placeholder and never blocks the sidebar.

## Kimi for Coding

```yaml
widgets:
  kimi:
    enabled: true
    style: nerd
    show_session: true
    show_weekly: true
    update_interval: 60
    position: bottom
    priority: 55
    fg: "#e0def4"
    bar_fg: "#9ccfd8"
```

Endpoint and key come from `TABBY_KIMI_URL` and `TABBY_KIMI_API_KEY`, or from
`url` and `api_key` in the block above.

## Writing your own

The widget interface lives in `pkg/config/config.go` and the render path in
`cmd/sidebar-renderer/main.go`. The shape is:

1. Add a config struct with an `Enabled bool` and your options.
2. Add a field to the `Widgets` struct.
3. Add a tick message and a `tea.Cmd` if the widget updates on a timer.
4. Start the tick in `Init()` when the widget is enabled.
5. Handle the tick in `Update()`.
6. Add a render function returning the widget's lines.
7. Call it from `View()` at the right position.

Keep anything slow, especially network calls, off the render path. Fetch on a
timer into a field and render from that field, the way the teamclaude widget
does.
