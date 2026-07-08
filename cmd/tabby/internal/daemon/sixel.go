package daemon

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// (hexToRGB lives in coordinator.go)

// gradientRGB builds raw 24-bit RGB pixel bytes for a left->right gradient bar,
// `width` px wide and `heightPx` tall (every row identical).
func gradientRGB(fromHex, toHex string, width, heightPx int) []byte {
	fr, fg, fb := hexToRGB(fromHex)
	tr, tg, tb := hexToRGB(toHex)
	d := width - 1
	if d < 1 {
		d = 1
	}
	row := make([]byte, 0, width*3)
	for x := 0; x < width; x++ {
		row = append(row,
			byte((fr*(d-x)+tr*x)/d),
			byte((fg*(d-x)+tg*x)/d),
			byte((fb*(d-x)+tb*x)/d),
		)
	}
	raw := make([]byte, 0, width*heightPx*3)
	for y := 0; y < heightPx; y++ {
		raw = append(raw, row...)
	}
	return raw
}

// kittyGradientBar returns a raw kitty graphics protocol sequence (APC _G ... ST)
// that transmits and displays a left->right gradient bar of `width`x`heightPx`
// pixels (24-bit RGB, f=24, a=T). The base64 payload is chunked at 4096 bytes with
// the m=1/m=0 continuation flag per the protocol. Wrap in tmuxPassthrough to send
// it through tmux to the terminal.
func kittyGradientBar(fromHex, toHex string, width, heightPx int) string {
	if width < 1 {
		width = 1
	}
	if heightPx < 1 {
		heightPx = 1
	}
	b64 := base64.StdEncoding.EncodeToString(gradientRGB(fromHex, toHex, width, heightPx))
	const chunk = 4096
	var out strings.Builder
	first := true
	for len(b64) > 0 {
		n := chunk
		if n > len(b64) {
			n = len(b64)
		}
		piece := b64[:n]
		b64 = b64[n:]
		more := 0
		if len(b64) > 0 {
			more = 1
		}
		if first {
			out.WriteString(fmt.Sprintf("\x1b_Gf=24,s=%d,v=%d,a=T,m=%d;%s\x1b\\", width, heightPx, more, piece))
			first = false
		} else {
			out.WriteString(fmt.Sprintf("\x1b_Gm=%d;%s\x1b\\", more, piece))
		}
	}
	return out.String()
}

// sixelGradientBar returns a raw sixel image (DCS q … ST) of a left->right
// gradient between two hex colours, `width` pixels wide and `heightPx` tall.
// Self-contained (no external encoder): the palette is quantised to a fixed
// number of steps and every column emits one full-height sixel of its step.
func sixelGradientBar(fromHex, toHex string, width, heightPx int) string {
	if width < 1 {
		width = 1
	}
	if heightPx < 1 {
		heightPx = 1
	}
	const steps = 24
	fr, fg, fb := hexToRGB(fromHex)
	tr, tg, tb := hexToRGB(toHex)

	var b strings.Builder
	b.WriteString("\x1bPq")                                  // DCS q — enter sixel
	b.WriteString(fmt.Sprintf(`"1;1;%d;%d`, width, heightPx)) // raster: 1:1 aspect, W x H
	// Palette: `steps` colours from -> to. Sixel colour components are 0-100.
	for i := 0; i < steps; i++ {
		d := steps - 1
		if d < 1 {
			d = 1
		}
		r := (fr*(d-i) + tr*i) / d
		g := (fg*(d-i) + tg*i) / d
		bl := (fb*(d-i) + tb*i) / d
		b.WriteString(fmt.Sprintf("#%d;2;%d;%d;%d", i, r*100/255, g*100/255, bl*100/255))
	}
	// Sixel data: 6 pixel-rows per band. Each column draws one full-height sixel
	// in its gradient-step colour.
	bands := (heightPx + 5) / 6
	for band := 0; band < bands; band++ {
		rows := heightPx - band*6
		if rows > 6 {
			rows = 6
		}
		ch := byte(0x3f + (1 << uint(rows)) - 1) // 0x3f + bitmask of `rows` set bits
		for x := 0; x < width; x++ {
			ci := x * steps / width
			if ci >= steps {
				ci = steps - 1
			}
			b.WriteString(fmt.Sprintf("#%d%c", ci, ch))
		}
		if band < bands-1 {
			b.WriteByte('-') // graphics newline (next 6-row band)
		}
	}
	b.WriteString("\x1b\\") // ST — leave sixel
	return b.String()
}

// tmuxPassthrough wraps raw terminal bytes so tmux forwards them verbatim to the
// outer terminal (requires `set -g allow-passthrough on`). Every ESC is doubled
// per the tmux DCS passthrough contract. This lets sixel reach the real terminal
// even when tmux itself was not built with native sixel support.
func tmuxPassthrough(s string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(s, "\x1b", "\x1b\x1b") + "\x1b\\"
}
