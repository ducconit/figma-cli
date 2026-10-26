package transformers

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type point2D struct {
	X float64
	Y float64
}

type gradStop struct {
	Color    RGBA
	Position float64
}

// ConvertGradientToCSS converts a Figma GRADIENT_LINEAR, GRADIENT_RADIAL, GRADIENT_ANGULAR,
// or GRADIENT_DIAMOND paint object into a standard CSS gradient string.
func ConvertGradientToCSS(gradient map[string]any) string {
	gradType := getString(gradient, "type")
	opacity := 1.0
	if op, ok := getFloat(gradient, "opacity"); ok {
		opacity = op
	}

	stops := extractGradientStops(gradient)
	if len(stops) == 0 {
		return ""
	}
	handles := extractGradientHandles(gradient)

	switch gradType {
	case "GRADIENT_LINEAR":
		return mapLinearGradient(handles, stops, opacity)
	case "GRADIENT_RADIAL":
		return mapRadialGradient(handles, stops, opacity)
	case "GRADIENT_ANGULAR":
		return mapAngularGradient(handles, stops, opacity)
	case "GRADIENT_DIAMOND":
		return mapDiamondGradient(handles, stops, opacity)
	default:
		return ""
	}
}

func extractGradientStops(gradient map[string]any) []gradStop {
	rawStops := getMapSlice(gradient, "gradientStops")
	out := make([]gradStop, 0, len(rawStops))
	for _, s := range rawStops {
		pos, ok := getFloat(s, "position")
		if !ok {
			continue
		}
		cMap, _ := s["color"].(map[string]any)
		c, ok := ExtractRGBA(cMap)
		if !ok {
			continue
		}
		out = append(out, gradStop{Color: c, Position: pos})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Position < out[j].Position
	})
	return out
}

func extractGradientHandles(gradient map[string]any) []point2D {
	rawHandles := getMapSlice(gradient, "gradientHandlePositions")
	out := make([]point2D, 0, len(rawHandles))
	for _, h := range rawHandles {
		x, _ := getFloat(h, "x")
		y, _ := getFloat(h, "y")
		out = append(out, point2D{X: x, Y: y})
	}
	return out
}

func mapLinearGradient(handles []point2D, stops []gradStop, opacity float64) string {
	start := point2D{X: 0, Y: 0}
	end := point2D{X: 0, Y: 1}
	if len(handles) >= 2 {
		start = handles[0]
		end = handles[1]
	}

	dx := end.X - start.X
	dy := end.Y - start.Y

	angle := math.Round(math.Atan2(dy, dx)*(180.0/math.Pi) + 90.0)
	if angle < 0 {
		angle += 360.0
	}
	angle = math.Mod(angle, 360.0)

	tMin, tMax := findExtendedLineIntersections(start, end)
	span := tMax - tMin
	if math.Abs(span) < 1e-9 {
		span = 1.0
	}

	stopStrings := make([]string, 0, len(stops))
	for _, st := range stops {
		extendedPos := clamp01((st.Position - tMin) / span)
		pct := int(math.Round(extendedPos * 100))
		colorStr := ConvertColor(st.Color, opacity)
		stopStrings = append(stopStrings, fmt.Sprintf("%s %d%%", colorStr, pct))
	}

	return fmt.Sprintf("linear-gradient(%gdeg, %s)", angle, strings.Join(stopStrings, ", "))
}

func findExtendedLineIntersections(start, end point2D) (float64, float64) {
	dx := end.X - start.X
	dy := end.Y - start.Y

	if math.Abs(dx) < 1e-9 && math.Abs(dy) < 1e-9 {
		return 0, 1
	}

	var tValues []float64
	eps := 1e-9

	// Intersect with y = 0 and y = 1
	if math.Abs(dy) > eps {
		for _, yEdge := range []float64{0.0, 1.0} {
			t := (yEdge - start.Y) / dy
			x := start.X + t*dx
			if x >= -eps && x <= 1.0+eps {
				tValues = append(tValues, t)
			}
		}
	}
	// Intersect with x = 0 and x = 1
	if math.Abs(dx) > eps {
		for _, xEdge := range []float64{0.0, 1.0} {
			t := (xEdge - start.X) / dx
			y := start.Y + t*dy
			if y >= -eps && y <= 1.0+eps {
				tValues = append(tValues, t)
			}
		}
	}

	if len(tValues) < 2 {
		return 0, 1
	}
	minT, maxT := tValues[0], tValues[0]
	for _, t := range tValues[1:] {
		if t < minT {
			minT = t
		}
		if t > maxT {
			maxT = t
		}
	}
	return minT, maxT
}

func mapRadialGradient(handles []point2D, stops []gradStop, opacity float64) string {
	cx, cy := 50, 50
	if len(handles) >= 1 {
		cx = int(math.Round(handles[0].X * 100))
		cy = int(math.Round(handles[0].Y * 100))
	}
	stopStrings := formatStandardStops(stops, opacity)
	return fmt.Sprintf("radial-gradient(circle at %d%% %d%%, %s)", cx, cy, strings.Join(stopStrings, ", "))
}

func mapAngularGradient(handles []point2D, stops []gradStop, opacity float64) string {
	cx, cy := 50, 50
	angle := 0.0
	if len(handles) >= 1 {
		cx = int(math.Round(handles[0].X * 100))
		cy = int(math.Round(handles[0].Y * 100))
	}
	if len(handles) >= 2 {
		dx := handles[1].X - handles[0].X
		dy := handles[1].Y - handles[0].Y
		angle = math.Round(math.Atan2(dy, dx)*(180.0/math.Pi) + 90.0)
		if angle < 0 {
			angle += 360.0
		}
	}
	stopStrings := formatStandardStops(stops, opacity)
	return fmt.Sprintf("conic-gradient(from %gdeg at %d%% %d%%, %s)", angle, cx, cy, strings.Join(stopStrings, ", "))
}

func mapDiamondGradient(handles []point2D, stops []gradStop, opacity float64) string {
	cx, cy := 50, 50
	if len(handles) >= 1 {
		cx = int(math.Round(handles[0].X * 100))
		cy = int(math.Round(handles[0].Y * 100))
	}
	stopStrings := formatStandardStops(stops, opacity)
	return fmt.Sprintf("radial-gradient(ellipse at %d%% %d%%, %s)", cx, cy, strings.Join(stopStrings, ", "))
}

func formatStandardStops(stops []gradStop, opacity float64) []string {
	out := make([]string, 0, len(stops))
	for _, st := range stops {
		pct := int(math.Round(st.Position * 100))
		colorStr := ConvertColor(st.Color, opacity)
		out = append(out, fmt.Sprintf("%s %d%%", colorStr, pct))
	}
	return out
}
