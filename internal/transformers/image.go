package transformers

import (
	"fmt"
	"math"
	"strings"
)

// ImageDownloadArgs holds metadata instructing the downloader how to process an image fill.
type ImageDownloadArgs struct {
	CropTransform           [][]float64 `json:"cropTransform,omitempty" yaml:"cropTransform,omitempty"`
	FilenameSuffix          string      `json:"filenameSuffix,omitempty" yaml:"filenameSuffix,omitempty"`
	NeedsCropping           bool        `json:"needsCropping,omitempty" yaml:"needsCropping,omitempty"`
	RequiresImageDimensions bool        `json:"requiresImageDimensions,omitempty" yaml:"requiresImageDimensions,omitempty"`
}

// PatternSource describes a node-backed pattern fill.
type PatternSource struct {
	Type   string `json:"type" yaml:"type"`
	NodeID string `json:"nodeId" yaml:"nodeId"`
}

// SimplifiedFill represents either a solid/gradient string or an object fill (IMAGE / PATTERN / GRADIENT).
type SimplifiedFill struct {
	ImageDownloadArgs  *ImageDownloadArgs `json:"imageDownloadArguments,omitempty" yaml:"imageDownloadArguments,omitempty"`
	PatternSource      *PatternSource     `json:"patternSource,omitempty" yaml:"patternSource,omitempty"`
	SolidColor         string             `json:"-" yaml:"-"`
	Type               string             `json:"type,omitempty" yaml:"type,omitempty"`
	ImageRef           string             `json:"imageRef,omitempty" yaml:"imageRef,omitempty"`
	GifRef             string             `json:"gifRef,omitempty" yaml:"gifRef,omitempty"`
	ScaleMode          string             `json:"scaleMode,omitempty" yaml:"scaleMode,omitempty"`
	ScalingFactor      float64            `json:"scalingFactor,omitempty" yaml:"scalingFactor,omitempty"`
	BackgroundSize     string             `json:"backgroundSize,omitempty" yaml:"backgroundSize,omitempty"`
	BackgroundRepeat   string             `json:"backgroundRepeat,omitempty" yaml:"backgroundRepeat,omitempty"`
	BackgroundPosition string             `json:"backgroundPosition,omitempty" yaml:"backgroundPosition,omitempty"`
	ObjectFit          string             `json:"objectFit,omitempty" yaml:"objectFit,omitempty"`
	Gradient           string             `json:"gradient,omitempty" yaml:"gradient,omitempty"`
	IsBackground       bool               `json:"isBackground,omitempty" yaml:"isBackground,omitempty"`
}

// ToSerializable converts SimplifiedFill into either a compact string (for solid colors) or a map.
func (f SimplifiedFill) ToSerializable() any {
	if f.SolidColor != "" {
		return f.SolidColor
	}
	m := make(map[string]any)
	if f.Type != "" {
		m["type"] = f.Type
	}
	if f.ImageRef != "" {
		m["imageRef"] = f.ImageRef
	}
	if f.GifRef != "" {
		m["gifRef"] = f.GifRef
	}
	if f.ScaleMode != "" {
		m["scaleMode"] = f.ScaleMode
	}
	if f.ScalingFactor != 0 {
		m["scalingFactor"] = f.ScalingFactor
	}
	if f.BackgroundSize != "" {
		m["backgroundSize"] = f.BackgroundSize
	}
	if f.BackgroundRepeat != "" {
		m["backgroundRepeat"] = f.BackgroundRepeat
	}
	if f.BackgroundPosition != "" {
		m["backgroundPosition"] = f.BackgroundPosition
	}
	if f.ObjectFit != "" {
		m["objectFit"] = f.ObjectFit
	}
	if f.IsBackground {
		m["isBackground"] = true
	}
	if f.Gradient != "" {
		m["gradient"] = f.Gradient
	}
	if f.PatternSource != nil {
		m["patternSource"] = map[string]any{
			"type":   f.PatternSource.Type,
			"nodeId": f.PatternSource.NodeID,
		}
	}
	if f.ImageDownloadArgs != nil {
		dl := make(map[string]any)
		if f.ImageDownloadArgs.NeedsCropping {
			dl["needsCropping"] = true
		}
		if f.ImageDownloadArgs.RequiresImageDimensions {
			dl["requiresImageDimensions"] = true
		}
		if len(f.ImageDownloadArgs.CropTransform) > 0 {
			dl["cropTransform"] = f.ImageDownloadArgs.CropTransform
		}
		if f.ImageDownloadArgs.FilenameSuffix != "" {
			dl["filenameSuffix"] = f.ImageDownloadArgs.FilenameSuffix
		}
		if len(dl) > 0 {
			m["imageDownloadArguments"] = dl
		}
	}
	return m
}

// GenerateTransformHash generates a deterministic 6-character hex hash from a 2x3 affine matrix.
func GenerateTransformHash(transform [][]float64) string {
	var parts []string
	for _, row := range transform {
		for _, v := range row {
			parts = append(parts, fmt.Sprintf("%g", PixelRound(v)))
		}
	}
	str := strings.Join(parts, "_")
	var acc int32
	for i := 0; i < len(str); i++ {
		acc = ((acc << 5) - acc + int32(str[i]))
	}
	u := uint32(acc)
	hex := fmt.Sprintf("%08x", u)
	if len(hex) > 6 {
		return hex[:6]
	}
	return hex
}

// ParseImagePaint translates a Figma IMAGE paint into a SimplifiedFill with CSS and crop metadata.
func ParseImagePaint(raw map[string]any, hasChildren bool) SimplifiedFill {
	scaleMode := getString(raw, "scaleMode")
	isBackground := hasChildren || scaleMode == "TILE"

	fill := SimplifiedFill{
		Type:      "IMAGE",
		ImageRef:  getString(raw, "imageRef"),
		GifRef:    getString(raw, "gifRef"),
		ScaleMode: scaleMode,
	}

	scalingFactor, hasScaling := getFloat(raw, "scalingFactor")
	if hasScaling {
		fill.ScalingFactor = scalingFactor
	}

	var dlArgs ImageDownloadArgs
	hasDLArgs := false

	switch scaleMode {
	case "FILL":
		if isBackground {
			fill.BackgroundSize = "cover"
			fill.BackgroundRepeat = "no-repeat"
			fill.IsBackground = true
		} else {
			fill.ObjectFit = "cover"
		}
	case "FIT":
		if isBackground {
			fill.BackgroundSize = "contain"
			fill.BackgroundRepeat = "no-repeat"
			fill.IsBackground = true
		} else {
			fill.ObjectFit = "contain"
		}
	case "TILE":
		fill.BackgroundRepeat = "repeat"
		if hasScaling && scalingFactor > 0 {
			fill.BackgroundSize = fmt.Sprintf("calc(var(--original-width) * %g) calc(var(--original-height) * %g)", scalingFactor, scalingFactor)
		} else {
			fill.BackgroundSize = "auto"
		}
		fill.IsBackground = true
		dlArgs.RequiresImageDimensions = true
		hasDLArgs = true
	case "STRETCH", "CROP":
		if isBackground {
			fill.BackgroundSize = "100% 100%"
			fill.BackgroundRepeat = "no-repeat"
			fill.IsBackground = true
		} else {
			fill.ObjectFit = "fill"
		}
	}

	if matrix, ok := extractTransformMatrix(raw["imageTransform"]); ok && !isIdentityMatrix(matrix) {
		dlArgs.NeedsCropping = true
		dlArgs.CropTransform = matrix
		dlArgs.FilenameSuffix = GenerateTransformHash(matrix)
		hasDLArgs = true
	}

	if hasDLArgs {
		fill.ImageDownloadArgs = &dlArgs
	}
	return fill
}

// ParsePatternPaint translates a Figma PATTERN paint into a SimplifiedFill.
func ParsePatternPaint(raw map[string]any) SimplifiedFill {
	sourceNodeID := getString(raw, "sourceNodeId")
	scalingFactor, hasScaling := getFloat(raw, "scalingFactor")
	bgSize := "100%"
	if hasScaling && scalingFactor > 0 {
		bgSize = fmt.Sprintf("%d%%", int(math.Round(scalingFactor*100)))
	}

	hAlign := mapPatternAlignH(getString(raw, "horizontalAlignment"))
	vAlign := mapPatternAlignV(getString(raw, "verticalAlignment"))
	bgPos := strings.TrimSpace(hAlign + " " + vAlign)

	return SimplifiedFill{
		Type: "PATTERN",
		PatternSource: &PatternSource{
			Type:   "IMAGE-PNG",
			NodeID: sourceNodeID,
		},
		BackgroundRepeat:   "repeat",
		BackgroundSize:     bgSize,
		BackgroundPosition: bgPos,
		IsBackground:       true,
	}
}

func mapPatternAlignH(a string) string {
	switch a {
	case "CENTER":
		return "center"
	case "END":
		return "right"
	default:
		return "left"
	}
}

func mapPatternAlignV(a string) string {
	switch a {
	case "CENTER":
		return "center"
	case "END":
		return "bottom"
	default:
		return "top"
	}
}

func extractTransformMatrix(v any) ([][]float64, bool) {
	rows, ok := v.([]any)
	if !ok {
		if typed, ok := v.([][]float64); ok && len(typed) == 2 {
			return typed, true
		}
		return nil, false
	}
	if len(rows) != 2 {
		return nil, false
	}
	out := make([][]float64, 2)
	for i, r := range rows {
		cols, ok := r.([]any)
		if !ok || len(cols) < 3 {
			return nil, false
		}
		out[i] = make([]float64, 3)
		for j := 0; j < 3; j++ {
			switch num := cols[j].(type) {
			case float64:
				out[i][j] = num
			case int:
				out[i][j] = float64(num)
			}
		}
	}
	return out, true
}

func isIdentityMatrix(m [][]float64) bool {
	if len(m) != 2 || len(m[0]) < 3 || len(m[1]) < 3 {
		return true
	}
	return math.Abs(m[0][0]-1) < 1e-6 &&
		math.Abs(m[0][1]) < 1e-6 &&
		math.Abs(m[0][2]) < 1e-6 &&
		math.Abs(m[1][0]) < 1e-6 &&
		math.Abs(m[1][1]-1) < 1e-6 &&
		math.Abs(m[1][2]) < 1e-6
}
