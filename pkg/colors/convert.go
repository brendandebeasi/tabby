package colors

import (
	"fmt"
	"strconv"
	"strings"
)

func HexToTmuxColor(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return "colour0"
	}
	r, errR := strconv.ParseInt(hex[0:2], 16, 64)
	g, errG := strconv.ParseInt(hex[2:4], 16, 64)
	b, errB := strconv.ParseInt(hex[4:6], 16, 64)
	if errR != nil || errG != nil || errB != nil {
		return "colour0"
	}

	r6 := int(r * 6 / 256)
	g6 := int(g * 6 / 256)
	b6 := int(b * 6 / 256)
	colorNum := 16 + 36*r6 + 6*g6 + b6
	return fmt.Sprintf("colour%d", colorNum)
}

func AdjustHex(hex string, amount float64) string {
	trimmed := strings.TrimPrefix(hex, "#")
	if len(trimmed) != 6 {
		return hex
	}
	r, errR := strconv.ParseInt(trimmed[0:2], 16, 64)
	g, errG := strconv.ParseInt(trimmed[2:4], 16, 64)
	b, errB := strconv.ParseInt(trimmed[4:6], 16, 64)
	if errR != nil || errG != nil || errB != nil {
		return hex
	}

	shift := func(v int64) int64 {
		adjusted := float64(v) + (255.0 * amount)
		if adjusted < 0 {
			adjusted = 0
		}
		if adjusted > 255 {
			adjusted = 255
		}
		return int64(adjusted)
	}

	nr := shift(r)
	ng := shift(g)
	nb := shift(b)

	return fmt.Sprintf("#%02x%02x%02x", nr, ng, nb)
}
