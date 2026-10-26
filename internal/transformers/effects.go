package transformers

import (
	"fmt"
	"strings"
)

// SimplifiedEffects holds CSS shadow and blur filter values.
type SimplifiedEffects struct {
	BoxShadow      string `json:"boxShadow,omitempty" yaml:"boxShadow,omitempty"`
	TextShadow     string `json:"textShadow,omitempty" yaml:"textShadow,omitempty"`
	Filter         string `json:"filter,omitempty" yaml:"filter,omitempty"`
	BackdropFilter string `json:"backdropFilter,omitempty" yaml:"backdropFilter,omitempty"`
}

// IsEmpty returns true when no effect properties were produced.
func (e SimplifiedEffects) IsEmpty() bool {
	return e.BoxShadow == "" && e.TextShadow == "" && e.Filter == "" && e.BackdropFilter == ""
}

// ToMap converts non-empty effect fields into a map for style registration.
func (e SimplifiedEffects) ToMap() map[string]any {
	m := make(map[string]any)
	if e.BoxShadow != "" {
		m["boxShadow"] = e.BoxShadow
	}
	if e.TextShadow != "" {
		m["textShadow"] = e.TextShadow
	}
	if e.Filter != "" {
		m["filter"] = e.Filter
	}
	if e.BackdropFilter != "" {
		m["backdropFilter"] = e.BackdropFilter
	}
	return m
}

// BuildSimplifiedEffects translates DROP_SHADOW, INNER_SHADOW, LAYER_BLUR, and BACKGROUND_BLUR into CSS.
// Blur radius is halved (radius / 2) to match Figma Dev Mode CSS output.
func BuildSimplifiedEffects(n map[string]any) SimplifiedEffects {
	var out SimplifiedEffects
	rawEffects := getMapSlice(n, "effects")
	if len(rawEffects) == 0 {
		return out
	}

	nodeType := getString(n, "type")
	var shadows []string
	var layerBlurs []string
	var bgBlurs []string

	for _, eff := range rawEffects {
		if !isVisibleMap(eff) {
			continue
		}
		effType := getString(eff, "type")
		switch effType {
		case "DROP_SHADOW":
			if s := formatShadow(eff, false); s != "" {
				shadows = append(shadows, s)
			}
		case "INNER_SHADOW":
			if nodeType != "TEXT" {
				if s := formatShadow(eff, true); s != "" {
					shadows = append(shadows, s)
				}
			}
		case "LAYER_BLUR":
			if radius, ok := getFloat(eff, "radius"); ok && radius > 0 {
				layerBlurs = append(layerBlurs, fmt.Sprintf("blur(%gpx)", PixelRound(radius/2.0)))
			}
		case "BACKGROUND_BLUR":
			if radius, ok := getFloat(eff, "radius"); ok && radius > 0 {
				bgBlurs = append(bgBlurs, fmt.Sprintf("blur(%gpx)", PixelRound(radius/2.0)))
			}
		}
	}

	if len(shadows) > 0 {
		joined := strings.Join(shadows, ", ")
		if nodeType == "TEXT" {
			out.TextShadow = joined
		} else {
			out.BoxShadow = joined
		}
	}
	if len(layerBlurs) > 0 {
		out.Filter = strings.Join(layerBlurs, " ")
	}
	if len(bgBlurs) > 0 {
		out.BackdropFilter = strings.Join(bgBlurs, " ")
	}
	return out
}

func formatShadow(eff map[string]any, inset bool) string {
	offsetMap, _ := eff["offset"].(map[string]any)
	ox, _ := getFloat(offsetMap, "x")
	oy, _ := getFloat(offsetMap, "y")
	radius, _ := getFloat(eff, "radius")
	spread, _ := getFloat(eff, "spread")
	colorMap, _ := eff["color"].(map[string]any)
	c, ok := ExtractRGBA(colorMap)
	if !ok {
		return ""
	}
	prefix := ""
	if inset {
		prefix = "inset "
	}
	if nodeSpreadAllowed(inset, spread) {
		return fmt.Sprintf("%s%gpx %gpx %gpx %gpx %s", prefix, PixelRound(ox), PixelRound(oy), PixelRound(radius), PixelRound(spread), FormatRGBAColor(c, 1.0))
	}
	return fmt.Sprintf("%s%gpx %gpx %gpx 0px %s", prefix, PixelRound(ox), PixelRound(oy), PixelRound(radius), FormatRGBAColor(c, 1.0))
}

func nodeSpreadAllowed(_ bool, _ float64) bool {
	return true
}
