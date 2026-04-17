#!/usr/bin/env python3
"""
render-capture.py - Capture tmux session and render to PNG using aha HTML output.

Usage:
    python3 render-capture.py [--session SESSION_ID] [--output OUTPUT.png]
"""

import subprocess
import sys
import re
import argparse
from html.parser import HTMLParser
from PIL import Image, ImageDraw, ImageFont
from typing import Optional

# SSH host for bastion
SSH_HOST = "shared-bastion"

# Terminal font settings
FONT_SIZE = 13
CHAR_W = 8    # pixels per character column
CHAR_H = 16   # pixels per character row

# Default terminal colors
DEFAULT_BG = (30, 30, 30)
DEFAULT_FG = (220, 220, 220)

# Monospace font paths
FONT_OPTIONS = [
    "/System/Library/Fonts/Menlo.ttc",
    "/System/Library/Fonts/Monaco.ttf",
    "/Library/Fonts/DejaVu Sans Mono.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
]

FONT_OPTIONS_BOLD = [
    "/System/Library/Fonts/Menlo.ttc",
]


def load_font(size=FONT_SIZE, bold=False):
    for path in FONT_OPTIONS:
        import os
        if os.path.exists(path):
            try:
                return ImageFont.truetype(path, size)
            except Exception:
                pass
    return ImageFont.load_default()


def parse_hex_color(hex_str: str) -> Optional[tuple]:
    """Parse #rrggbb to (r,g,b) tuple."""
    if not hex_str:
        return None
    h = hex_str.lstrip('#')
    if len(h) == 6:
        try:
            return tuple(int(h[i:i+2], 16) for i in (0, 2, 4))
        except ValueError:
            pass
    return None


def parse_inline_style(style: str) -> dict:
    """Parse 'color:#xxx;background-color:#xxx;font-weight:bold' into dict."""
    result = {}
    for part in style.split(';'):
        part = part.strip()
        if ':' in part:
            k, v = part.split(':', 1)
            result[k.strip()] = v.strip()
    return result


class AhaSpan:
    """Represents a styled text span from aha HTML."""
    def __init__(self, text='', fg=None, bg=None, bold=False):
        self.text = text
        self.fg = fg  # (r,g,b) or None for default
        self.bg = bg  # (r,g,b) or None for default
        self.bold = bold


class AhaParser(HTMLParser):
    """Parse aha HTML output into a list of AhaSpan objects."""

    def __init__(self):
        super().__init__()
        self.spans = []
        self._style_stack = [{}]  # stack of style dicts

    def _current_style(self):
        # Merge all styles on stack
        merged = {}
        for s in self._style_stack:
            merged.update(s)
        return merged

    def handle_starttag(self, tag, attrs):
        if tag == 'span':
            style_str = dict(attrs).get('style', '')
            style = parse_inline_style(style_str)
            self._style_stack.append(style)

    def handle_endtag(self, tag):
        if tag == 'span' and len(self._style_stack) > 1:
            self._style_stack.pop()

    def handle_data(self, data):
        if not data:
            return
        style = self._current_style()
        fg = parse_hex_color(style.get('color'))
        bg = parse_hex_color(style.get('background-color'))
        bold = style.get('font-weight') == 'bold'
        self.spans.append(AhaSpan(text=data, fg=fg, bg=bg, bold=bold))

    def handle_entityref(self, name):
        entities = {'amp': '&', 'lt': '<', 'gt': '>', 'quot': '"', 'nbsp': ' '}
        self.handle_data(entities.get(name, ''))

    def handle_charref(self, name):
        try:
            if name.startswith('x'):
                c = chr(int(name[1:], 16))
            else:
                c = chr(int(name))
            self.handle_data(c)
        except (ValueError, OverflowError):
            pass


def render_spans_to_image(spans: list, width: int, height: int,
                           font, bold_font) -> Image.Image:
    """Render AhaSpan list to PIL Image of given character dimensions."""
    img = Image.new('RGB', (width * CHAR_W, height * CHAR_H), DEFAULT_BG)
    draw = ImageDraw.Draw(img)

    col = 0
    row = 0

    for span in spans:
        for ch in span.text:
            if ch == '\n':
                row += 1
                col = 0
                if row >= height:
                    return img
                continue
            if ch == '\r':
                col = 0
                continue
            if ch == '\t':
                # Tab = advance to next 8-col boundary
                col = ((col // 8) + 1) * 8
                continue

            if col >= width:
                # Wrap (shouldn't happen normally with tmux -J)
                continue

            x = col * CHAR_W
            y = row * CHAR_H

            fg = span.fg or DEFAULT_FG
            bg = span.bg or DEFAULT_BG

            # Draw background
            draw.rectangle([x, y, x + CHAR_W, y + CHAR_H], fill=bg)

            # Draw character (skip space to save time)
            if ch and ch != ' ':
                f = bold_font if span.bold else font
                try:
                    draw.text((x, y), ch, fill=fg, font=f)
                except Exception:
                    pass

            col += 1

    return img


def ssh_run(cmd: str) -> str:
    """Run command on shared-bastion, return stdout."""
    result = subprocess.run(
        ["ssh", SSH_HOST, cmd],
        capture_output=True, text=True
    )
    return result.stdout


def get_pane_info(session: str) -> list:
    """Return pane info for the active window of session."""
    out = ssh_run(
        f"tmux list-panes -t '{session}' -F "
        "'#{pane_id} #{pane_left} #{pane_top} #{pane_width} #{pane_height}'"
    )
    panes = []
    for line in out.strip().splitlines():
        parts = line.strip().split()
        if len(parts) == 5:
            panes.append({
                'id': parts[0],
                'left': int(parts[1]),
                'top': int(parts[2]),
                'width': int(parts[3]),
                'height': int(parts[4]),
            })
    return panes


def get_session_size(session: str) -> tuple:
    out = ssh_run(f"tmux display -t '{session}' -p '#{{window_width}} #{{window_height}}'")
    parts = out.strip().split()
    if len(parts) == 2:
        return int(parts[0]), int(parts[1])
    return 188, 51


def capture_pane_as_html(pane_id: str, width: int, height: int) -> str:
    """Capture pane via aha and return HTML spans."""
    out = ssh_run(
        f"tmux capture-pane -t '{pane_id}' -p -e -J 2>/dev/null | aha --no-header 2>/dev/null"
    )
    return out


def render_session(session: str, output_path: str):
    print(f"Capturing session {session}...")

    term_w, term_h = get_session_size(session)
    print(f"Terminal size: {term_w}x{term_h}")

    panes = get_pane_info(session)
    print(f"Found {len(panes)} panes")

    font = load_font(FONT_SIZE)
    bold_font = load_font(FONT_SIZE)

    img_w = term_w * CHAR_W
    img_h = term_h * CHAR_H
    full_img = Image.new('RGB', (img_w, img_h), DEFAULT_BG)

    for pane in panes:
        print(f"  Pane {pane['id']} ({pane['width']}x{pane['height']} at {pane['left']},{pane['top']})...")

        html = capture_pane_as_html(pane['id'], pane['width'], pane['height'])

        parser = AhaParser()
        parser.feed(html)
        spans = parser.spans

        pane_img = render_spans_to_image(
            spans, pane['width'], pane['height'], font, bold_font
        )

        px = pane['left'] * CHAR_W
        py = pane['top'] * CHAR_H
        full_img.paste(pane_img, (px, py))

    # Draw thin separator lines at pane boundaries
    draw = ImageDraw.Draw(full_img)
    for pane in panes:
        rx = (pane['left'] + pane['width']) * CHAR_W
        ry = (pane['top'] + pane['height']) * CHAR_H
        if rx < img_w:
            draw.line([(rx, pane['top']*CHAR_H), (rx, ry)], fill=(80, 80, 80))
        if ry < img_h:
            draw.line([(pane['left']*CHAR_W, ry), (rx, ry)], fill=(80, 80, 80))

    full_img.save(output_path)
    print(f"Saved: {output_path}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--session', '-s', default='')
    parser.add_argument('--output', '-o', default='/tmp/tabby-capture.png')
    args = parser.parse_args()

    session = args.session
    if not session:
        out = ssh_run("tmux list-sessions -F '#{session_last_attached} #{session_id}' 2>/dev/null")
        lines = sorted(out.strip().splitlines(), reverse=True)
        if not lines:
            print("No sessions found", file=sys.stderr)
            sys.exit(1)
        session = lines[0].split()[-1]
        print(f"Session: {session}")

    render_session(session, args.output)


if __name__ == '__main__':
    main()
