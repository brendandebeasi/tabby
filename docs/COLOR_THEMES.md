# Color Theme Guide

This guide explains how to configure colors for window groups in tabby.

## Table of Contents

- [Quick Start](#quick-start)
- [How Auto-Derivation Works](#how-auto-derivation-works)
- [Advanced Configuration](#advanced-configuration)
- [Terminal Background Detection](#terminal-background-detection)
- [Color Selection Tips](#color-selection-tips)
- [Accessibility](#accessibility)
- [Troubleshooting](#troubleshooting)
- [Examples](#examples)
- [Color Reference](#color-reference)
- [Technical Details](#technical-details)
- [Migration from Old Config](#migration-from-old-config)
- [Further Reading](#further-reading)

## Quick Start

### Minimal Configuration (Recommended)

Just specify a base color for each group. Everything else is auto-derived:

```yaml
groups:
  - name: "StudioDome"
    theme:
      bg: "#8b1a1a"  # Dark red - everything else auto-generated

  - name: "Gunpowder"
    theme:
      bg: "#7f8c8d"  # Gray - everything else auto-generated
```

### No Configuration

If you don't specify colors at all, tabby uses a pleasant default palette:

- Group 0: Blue (#3498db)
- Group 1: Green (#2ecc71)
- Group 2: Red (#e74c3c)
- Group 3: Purple (#9b59b6)
- Group 4: Orange (#f39c12)
- And 7 more colors...

## How Auto-Derivation Works

When you specify only a base color (`bg`), tabby automatically generates:

1. **Foreground** (`fg`) - White or black text, whichever has better contrast
2. **Active Background** (`active_bg`) - Saturated, vibrant version of base color
3. **Active Foreground** (`active_fg`) - Text color for active tabs
4. **Inactive Background** (`inactive_bg`) - Subtle, desaturated version
5. **Inactive Foreground** (`inactive_fg`) - Text color for inactive tabs

All derived colors:
- Work on both dark and light terminal backgrounds
- Meet WCAG AA contrast requirements (4.5:1 ratio)
- Maintain visual hierarchy (active > base > inactive)

## Advanced Configuration

### Partial Override

Override specific colors while keeping others auto-derived:

```yaml
groups:
  - name: "Project"
    theme:
      bg: "#3498db"
      active_fg: "#ffff00"  # Custom yellow text for active tabs
      # fg, active_bg, inactive_bg, inactive_fg auto-derived
```

### Full Control

Specify every color explicitly:

```yaml
groups:
  - name: "Project"
    theme:
      bg: "#3498db"           # Base background
      fg: "#ffffff"           # Base text
      active_bg: "#2980b9"    # Active tab background
      active_fg: "#ffffff"    # Active tab text
      inactive_bg: "#5dade2"  # Inactive tab background
      inactive_fg: "#ecf0f1"  # Inactive tab text
```

## Terminal Background Detection

Tabby auto-detects whether your terminal has a dark or light background and adjusts color derivation accordingly.

### Override Detection

Force dark or light mode in your config:

```yaml
sidebar:
  theme_mode: auto  # Options: auto (default), dark, light
```

## Color Selection Tips

### Choosing a Base Color

- **High Contrast**: Use saturated colors (#3498db, #2ecc71, #e74c3c)
- **Professional**: Use muted colors (#7f8c8d, #34495e)
- **Vibrant**: Use bright colors (#f39c12, #9b59b6)

### Testing Your Colors

1. Start with just `bg` specified
2. See how the auto-derived colors look
3. Override specific colors if needed
4. Test on both dark and light terminal backgrounds

### Common Patterns

**Semantic Colors**:
```yaml
groups:
  - name: "Production"
    theme:
      bg: "#e74c3c"  # Red - danger/production

  - name: "Development"
    theme:
      bg: "#2ecc71"  # Green - safe/development

  - name: "Staging"
    theme:
      bg: "#f39c12"  # Orange - warning/staging
```

**Project-Based Colors**:
```yaml
groups:
  - name: "Frontend"
    theme:
      bg: "#3498db"  # Blue - frontend

  - name: "Backend"
    theme:
      bg: "#9b59b6"  # Purple - backend

  - name: "DevOps"
    theme:
      bg: "#16a085"  # Teal - devops
```

## Accessibility

All auto-derived colors meet **WCAG AA** accessibility standards:
- Minimum 4.5:1 contrast ratio for normal text
- Automatic adjustment if specified colors don't meet requirements
- Works for users with normal vision and mild visual impairments

For enhanced accessibility (WCAG AAA), specify high-contrast colors explicitly.

## Troubleshooting

### Colors Look Washed Out

Your terminal might have a light background. Try:
```yaml
sidebar:
  theme_mode: light  # Use light-optimized derivation
```

### Active Tabs Not Visible Enough

Increase saturation by specifying active_bg explicitly:
```yaml
theme:
  bg: "#3498db"
  active_bg: "#2980b9"  # Darker, more saturated
```

### Text Hard to Read

Check your terminal's color profile. Tabby assumes 256-color support. If text is hard to read:
1. Try a different base color
2. Override fg/active_fg explicitly
3. Set theme_mode to match your terminal

## Examples

### Example 1: Minimal Config
```yaml
groups:
  - name: "Work"
    theme:
      bg: "#3498db"
```

Result (on dark terminal):
- bg: #3498db (your specified blue)
- fg: #000000 (auto: black text)
- active_bg: #35acfc (auto: brighter blue)
- active_fg: #000000 (auto: black text)
- inactive_bg: #3a7daa (auto: muted blue)
- inactive_fg: #000000 (auto: black text)

### Example 2: Custom Active Color
```yaml
groups:
  - name: "Work"
    theme:
      bg: "#3498db"
      active_bg: "#e74c3c"  # Red active tabs instead of blue
```

Result:
- bg: #3498db (your specified blue)
- fg: #000000 (auto)
- active_bg: #e74c3c (your specified red)
- active_fg: #ffffff (auto: white text for red background)
- inactive_bg: #3a7daa (auto: muted blue)
- inactive_fg: #000000 (auto)

### Example 3: High Contrast
```yaml
groups:
  - name: "Work"
    theme:
      bg: "#000000"
      fg: "#ffffff"
      active_bg: "#ffffff"
      active_fg: "#000000"
```

Result: Pure black and white theme, maximum contrast.

## Color Reference

Default palette (used when no colors specified):

| Index | Color | Name | Usage |
|-------|-------|------|-------|
| 0 | #3498db | Blue | Professional, calm |
| 1 | #2ecc71 | Green | Success, development |
| 2 | #e74c3c | Red | Error, production |
| 3 | #9b59b6 | Purple | Creative, backend |
| 4 | #f39c12 | Orange | Warning, staging |
| 5 | #1abc9c | Turquoise | Fresh, modern |
| 6 | #e67e22 | Carrot | Energy, active |
| 7 | #34495e | Dark gray | Neutral, serious |
| 8 | #16a085 | Green sea | Nature, devops |
| 9 | #c0392b | Pomegranate | Deep red, critical |
| 10 | #8e44ad | Wisteria | Royal, important |
| 11 | #27ae60 | Nephritis | Growth, testing |

## Technical Details

### Color Derivation Algorithm

**Active Background**:
- Saturation: 140% (more vibrant)
- Lightness: Adjusted for terminal background

**Inactive Background**:
- Saturation: 70% (more subtle)
- Lightness: Slightly adjusted

**Text Colors**:
- Auto-selected for maximum contrast
- WCAG AA compliant (4.5:1 minimum)

### Contrast Calculation

Tabby uses the WCAG 2.0 relative luminance formula:
```
L = 0.2126 * R + 0.7152 * G + 0.0722 * B
Contrast Ratio = (L1 + 0.05) / (L2 + 0.05)
```

Where R, G, B are gamma-corrected RGB values.

## Migration from Old Config

Old configs continue to work! If you had:

```yaml
groups:
  - name: "Project"
    theme:
      bg: "#000000"
      fg: "#ffffff"
      active_bg: "#333333"
      active_fg: "#ffffff"
```

This still works exactly as before. New auto-derivation only applies to missing values.

## Further Reading

- [WCAG 2.0 Contrast Guidelines](https://www.w3.org/WAI/WCAG21/Understanding/contrast-minimum.html)
- [HSL Color Space](https://en.wikipedia.org/wiki/HSL_and_HSV)
- [Terminal Color Support](https://gist.github.com/XVilka/8346728)
