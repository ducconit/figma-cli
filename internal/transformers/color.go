package transformers

import (
	"fmt"
	"math"
	"strings"
)

// RGBA represents a Figma color in [0, 1] float channels.
type RGBA struct {
	R float64
	G float64
	B float64
	A float64
}

// PixelRound rounds a float to at most 2 decimal places.
func PixelRound(num float64) float64 {
	return math.Round(num*100) / 100
}

// EmRound rounds a float to at most 4 decimal places.
func EmRound(num float64) float64 {
	return math.Round(num*10000) / 10000
}

// FormatPx formats a rounded pixel value as "<num>px".
func FormatPx(num float64) string {
	return fmt.Sprintf("%gpx", PixelRound(num))
}

// ExtractRGBA parses a Figma color map into RGBA (defaulting alpha to 1.0).
func ExtractRGBA(m map[string]any) (RGBA, bool) {
	if m == nil {
		return RGBA{}, false
	}
	r, okR := getFloat(m, "r")
	g, okG := getFloat(m, "g")
	b, okB := getFloat(m, "b")
	if !okR || !okG || !okB {
		return RGBA{}, false
	}
	a, okA := getFloat(m, "a")
	if !okA {
		a = 1.0
	}
	return RGBA{R: r, G: g, B: b, A: a}, true
}

// ConvertColor formats an RGBA + paint opacity into either "#RRGGBB" (when alpha==1) or "rgba(r, g, b, a)".
func ConvertColor(color RGBA, opacity float64) string {
	r := int(math.Round(clamp01(color.R) * 255))
	g := int(math.Round(clamp01(color.G) * 255))
	b := int(math.Round(clamp01(color.B) * 255))
	a := math.Round(clamp01(opacity*color.A)*100) / 100

	if a >= 1.0 {
		return fmt.Sprintf("#%02X%02X%02X", r, g, b)
	}
	return fmt.Sprintf("rgba(%d, %d, %d, %g)", r, g, b, a)
}

// FormatRGBAColor always formats as "#RRGGBB" when alpha==1 or "rgba(r, g, b, a)".
func FormatRGBAColor(color RGBA, opacity float64) string {
	return ConvertColor(color, opacity)
}

// FlattenSolidFills blends a stack of visible SOLID fills (with NORMAL/PASS_THROUGH blendMode)
// using Porter-Duff Source-Over alpha compositing into a single resolved color string.
func FlattenSolidFills(visibleFills []map[string]any) (string, bool) {
	if len(visibleFills) == 0 {
		return "", false
	}

	var acc RGBA
	hasBase := false

	for _, fill := range visibleFills {
		if getString(fill, "type") != "SOLID" {
			return "", false
		}
		bm := getString(fill, "blendMode")
		if bm != "" && bm != "NORMAL" && bm != "PASS_THROUGH" {
			return "", false
		}
		colorMap, ok := fill["color"].(map[string]any)
		if !ok {
			return "", false
		}
		c, ok := ExtractRGBA(colorMap)
		if !ok {
			return "", false
		}
		opacity := 1.0
		if op, ok := getFloat(fill, "opacity"); ok {
			opacity = op
		}
		top := RGBA{
			R: c.R,
			G: c.G,
			B: c.B,
			A: clamp01(c.A * opacity),
		}
		if !hasBase {
			acc = top
			hasBase = true
		} else {
			acc = blendSourceOver(acc, top)
		}
	}

	if !hasBase {
		return "", false
	}
	return ConvertColor(acc, 1.0), true
}

func blendSourceOver(bottom, top RGBA) RGBA {
	aOut := top.A + bottom.A*(1.0-top.A)
	if aOut <= 0 {
		return RGBA{}
	}
	return RGBA{
		R: (top.R*top.A + bottom.R*bottom.A*(1.0-top.A)) / aOut,
		G: (top.G*top.A + bottom.G*bottom.A*(1.0-top.A)) / aOut,
		B: (top.B*top.A + bottom.B*bottom.A*(1.0-top.A)) / aOut,
		A: aOut,
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// GenerateCSSShorthand converts top, right, bottom, left pixel values into minimal CSS shorthand.
func GenerateCSSShorthand(top, right, bottom, left float64, ignoreZero bool) string {
	t := PixelRound(top)
	r := PixelRound(right)
	b := PixelRound(bottom)
	l := PixelRound(left)
	if ignoreZero && t == 0 && r == 0 && b == 0 && l == 0 {
		return ""
	}
	if t == r && r == b && b == l {
		return fmt.Sprintf("%gpx", t)
	}
	if t == b && r == l {
		return fmt.Sprintf("%gpx %gpx", t, r)
	}
	if r == l {
		return fmt.Sprintf("%gpx %gpx %gpx", t, r, b)
	}
	return fmt.Sprintf("%gpx %gpx %gpx %gpx", t, r, b, l)
}

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	switch v := m[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func getBool(m map[string]any, key string, fallback bool) bool {
	if m == nil {
		return fallback
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return fallback
}

func isVisibleMap(m map[string]any) bool {
	return getBool(m, "visible", true)
}

func getMapSlice(m map[string]any, key string) []map[string]any {
	if m == nil {
		return nil
	}
	raw, ok := m[key].([]any)
	if !ok {
		if typed, ok := m[key].([]map[string]any); ok {
			return typed
		}
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if mp, ok := item.(map[string]any); ok {
			out = append(out, mp)
		}
	}
	return out
}

func trimNonEmpty(s string) string {
	return strings.TrimSpace(s)
}
