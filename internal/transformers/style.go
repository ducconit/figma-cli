package transformers

import (
	"fmt"
	"slices"
)

// SimplifiedStrokes represents stroke colors and stroke geometry on a node.
type SimplifiedStrokes struct {
	Colors        []any     `json:"colors,omitempty" yaml:"colors,omitempty"`
	StrokeWeight  string    `json:"strokeWeight,omitempty" yaml:"strokeWeight,omitempty"`
	StrokeWeights string    `json:"strokeWeights,omitempty" yaml:"strokeWeights,omitempty"`
	StrokeAlign   string    `json:"strokeAlign,omitempty" yaml:"strokeAlign,omitempty"`
	StrokeDashes  []float64 `json:"strokeDashes,omitempty" yaml:"strokeDashes,omitempty"`
}

// ParsePaint converts a single Figma paint object into a SimplifiedFill.
func ParsePaint(raw map[string]any, hasChildren bool) (SimplifiedFill, bool) {
	if !isVisibleMap(raw) {
		return SimplifiedFill{}, false
	}
	paintType := getString(raw, "type")
	switch paintType {
	case "SOLID":
		cMap, _ := raw["color"].(map[string]any)
		c, ok := ExtractRGBA(cMap)
		if !ok {
			return SimplifiedFill{}, false
		}
		opacity := 1.0
		if op, ok := getFloat(raw, "opacity"); ok {
			opacity = op
		}
		return SimplifiedFill{SolidColor: ConvertColor(c, opacity)}, true
	case "IMAGE":
		return ParseImagePaint(raw, hasChildren), true
	case "PATTERN":
		return ParsePatternPaint(raw), true
	case "GRADIENT_LINEAR", "GRADIENT_RADIAL", "GRADIENT_ANGULAR", "GRADIENT_DIAMOND":
		css := ConvertGradientToCSS(raw)
		if css == "" {
			return SimplifiedFill{}, false
		}
		return SimplifiedFill{
			Type:     paintType,
			Gradient: css,
		}, true
	default:
		return SimplifiedFill{}, false
	}
}

// BuildSimplifiedStrokes extracts visible stroke colors, weights, dashes, and non-default alignment.
// Note: strokeAlign == "INSIDE" is intentionally omitted because CSS border-box defaults to inside.
func BuildSimplifiedStrokes(n map[string]any, hasChildren bool) SimplifiedStrokes {
	var out SimplifiedStrokes
	rawStrokes := getMapSlice(n, "strokes")
	if len(rawStrokes) == 0 {
		return out
	}

	var parsed []any
	for _, s := range rawStrokes {
		if fill, ok := ParsePaint(s, hasChildren); ok {
			parsed = append(parsed, fill.ToSerializable())
		}
	}
	if len(parsed) == 0 {
		return out
	}
	// Reverse to match CSS top-first layer ordering
	slices.Reverse(parsed)
	out.Colors = parsed

	if indiv, ok := n["individualStrokeWeights"].(map[string]any); ok {
		top, _ := getFloat(indiv, "top")
		right, _ := getFloat(indiv, "right")
		bottom, _ := getFloat(indiv, "bottom")
		left, _ := getFloat(indiv, "left")
		if sh := GenerateCSSShorthand(top, right, bottom, left, true); sh != "" {
			out.StrokeWeights = sh
		}
	} else if sw, ok := getFloat(n, "strokeWeight"); ok && sw > 0 {
		out.StrokeWeight = fmt.Sprintf("%gpx", PixelRound(sw))
	}

	if dashesRaw, ok := n["strokeDashes"].([]any); ok && len(dashesRaw) > 0 {
		dashes := make([]float64, 0, len(dashesRaw))
		for _, d := range dashesRaw {
			switch num := d.(type) {
			case float64:
				dashes = append(dashes, PixelRound(num))
			case int:
				dashes = append(dashes, float64(num))
			}
		}
		if len(dashes) > 0 {
			out.StrokeDashes = dashes
		}
	}

	align := getString(n, "strokeAlign")
	if align == "OUTSIDE" || align == "CENTER" {
		out.StrokeAlign = align
	}

	return out
}
