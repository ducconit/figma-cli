package transformers

import (
	"strings"
	"testing"
)

func TestFlattenSolidFillsAndColorConversion(t *testing.T) {
	// 1. Single opaque solid fill -> #RRGGBB
	fills := []map[string]any{
		{
			"type":  "SOLID",
			"color": map[string]any{"r": 1.0, "g": 0.0, "b": 0.0, "a": 1.0},
		},
	}
	hex, ok := FlattenSolidFills(fills)
	if !ok || hex != "#FF0000" {
		t.Fatalf("expected #FF0000, got %s (%v)", hex, ok)
	}

	// 2. White bottom + 50% black top -> #808080
	stacked := []map[string]any{
		{
			"type":  "SOLID",
			"color": map[string]any{"r": 1.0, "g": 1.0, "b": 1.0, "a": 1.0},
		},
		{
			"type":    "SOLID",
			"opacity": 0.5,
			"color":   map[string]any{"r": 0.0, "g": 0.0, "b": 0.0, "a": 1.0},
		},
	}
	blended, ok := FlattenSolidFills(stacked)
	if !ok || blended != "#808080" {
		t.Fatalf("expected blended #808080, got %s (%v)", blended, ok)
	}
}

func TestGradientConversion(t *testing.T) {
	stops := []any{
		map[string]any{"position": 0.0, "color": map[string]any{"r": 1.0, "g": 0.0, "b": 0.0, "a": 1.0}},
		map[string]any{"position": 1.0, "color": map[string]any{"r": 0.0, "g": 0.0, "b": 1.0, "a": 1.0}},
	}
	handles := []any{
		map[string]any{"x": 0.0, "y": 0.5},
		map[string]any{"x": 1.0, "y": 0.5},
		map[string]any{"x": 0.5, "y": 1.0},
	}

	// 1. Linear
	linear := map[string]any{
		"type":                    "GRADIENT_LINEAR",
		"gradientHandlePositions": handles[:2],
		"gradientStops":           stops,
	}
	if css := ConvertGradientToCSS(linear); css != "linear-gradient(90deg, #FF0000 0%, #0000FF 100%)" {
		t.Fatalf("unexpected linear gradient CSS: %s", css)
	}

	// 2. Radial
	radial := map[string]any{
		"type":                    "GRADIENT_RADIAL",
		"gradientHandlePositions": handles,
		"gradientStops":           stops,
	}
	if css := ConvertGradientToCSS(radial); !strings.HasPrefix(css, "radial-gradient(circle at 0% 50%, #FF0000 0%, #0000FF 100%)") {
		t.Fatalf("unexpected radial gradient CSS: %s", css)
	}

	// 3. Angular (conic)
	angular := map[string]any{
		"type":                    "GRADIENT_ANGULAR",
		"gradientHandlePositions": handles,
		"gradientStops":           stops,
	}
	if css := ConvertGradientToCSS(angular); !strings.HasPrefix(css, "conic-gradient(from ") {
		t.Fatalf("unexpected angular gradient CSS: %s", css)
	}

	// 4. Diamond
	diamond := map[string]any{
		"type":                    "GRADIENT_DIAMOND",
		"gradientHandlePositions": handles,
		"gradientStops":           stops,
	}
	if css := ConvertGradientToCSS(diamond); !strings.HasPrefix(css, "radial-gradient(ellipse at 0% 50%, #FF0000 0%, #0000FF 100%)") {
		t.Fatalf("unexpected diamond gradient CSS: %s", css)
	}
}

func TestImagePatternAndStrokes(t *testing.T) {
	// 1. Image fill with TILE scaling + non-identity cropTransform
	tileImg := map[string]any{
		"type":          "IMAGE",
		"imageRef":      "img_ref_1",
		"scaleMode":     "TILE",
		"scalingFactor": 0.5,
		"imageTransform": []any{
			[]any{0.5, 0.0, 0.25},
			[]any{0.0, 0.5, 0.1},
		},
	}
	pf, ok := ParsePaint(tileImg, false)
	if !ok || pf.Type != "IMAGE" || !pf.IsBackground || pf.ImageDownloadArgs == nil || !pf.ImageDownloadArgs.NeedsCropping || !pf.ImageDownloadArgs.RequiresImageDimensions {
		t.Fatalf("unexpected TILE image paint: %+v", pf)
	}
	ser, isMap := pf.ToSerializable().(map[string]any)
	if !isMap || ser["backgroundRepeat"] != "repeat" || !strings.Contains(ser["backgroundSize"].(string), "var(--original-width)") {
		t.Fatalf("unexpected serialized TILE fill: %+v", ser)
	}

	// 2. FIT and STRETCH modes (leaf vs container)
	fitLeaf := ParseImagePaint(map[string]any{"type": "IMAGE", "imageRef": "r1", "scaleMode": "FIT"}, false)
	if fitLeaf.ObjectFit != "contain" {
		t.Fatalf("expected objectFit=contain, got %+v", fitLeaf)
	}
	fitBg := ParseImagePaint(map[string]any{"type": "IMAGE", "imageRef": "r1", "scaleMode": "FIT"}, true)
	if fitBg.BackgroundSize != "contain" || !fitBg.IsBackground {
		t.Fatalf("expected backgroundSize=contain, got %+v", fitBg)
	}
	stretchBg := ParseImagePaint(map[string]any{"type": "IMAGE", "imageRef": "r1", "scaleMode": "STRETCH"}, true)
	if stretchBg.BackgroundSize != "100% 100%" {
		t.Fatalf("expected backgroundSize=100%% 100%%, got %+v", stretchBg)
	}

	// 3. Pattern paint
	patPaint, ok := ParsePaint(map[string]any{
		"type":                "PATTERN",
		"sourceNodeId":        "99:1",
		"scalingFactor":       0.75,
		"horizontalAlignment": "CENTER",
		"verticalAlignment":   "END",
	}, false)
	if !ok || patPaint.BackgroundSize != "75%" || patPaint.BackgroundPosition != "center bottom" {
		t.Fatalf("unexpected pattern paint: %+v", patPaint)
	}
	patSer := patPaint.ToSerializable().(map[string]any)
	if patSer["type"] != "PATTERN" {
		t.Fatalf("unexpected pattern serializable: %+v", patSer)
	}

	// 4. Strokes with individualStrokeWeights, strokeDashes, and strokeAlign=OUTSIDE
	strokeNode := map[string]any{
		"strokes": []any{
			map[string]any{
				"type":  "SOLID",
				"color": map[string]any{"r": 0.0, "g": 1.0, "b": 0.0, "a": 1.0},
			},
		},
		"individualStrokeWeights": map[string]any{
			"top": 1.0, "right": 2.0, "bottom": 1.0, "left": 2.0,
		},
		"strokeDashes": []any{4.0, 2},
		"strokeAlign":  "OUTSIDE",
	}
	strokes := BuildSimplifiedStrokes(strokeNode, false)
	if len(strokes.Colors) != 1 || strokes.StrokeWeights != "1px 2px" || strokes.StrokeAlign != "OUTSIDE" || len(strokes.StrokeDashes) != 2 {
		t.Fatalf("unexpected strokes: %+v", strokes)
	}
}

func TestComponentTransformers(t *testing.T) {
	instanceNode := map[string]any{
		"componentProperties": map[string]any{
			"Label#12:0":   map[string]any{"type": "TEXT", "value": "Buy Now"},
			"On Sale#12:1": map[string]any{"type": "BOOLEAN", "value": true},
			"Icon#12:2":    map[string]any{"type": "INSTANCE_SWAP", "value": "99:1"},
		},
		"componentPropertyReferences": map[string]any{
			"visible":    "On Sale#12:1",
			"characters": "Label#12:0",
		},
	}
	props := SimplifyComponentProperties(instanceNode)
	if props["Label"] != "Buy Now" || props["On Sale"] != true || len(props) != 2 {
		t.Fatalf("unexpected simplified component properties: %+v", props)
	}
	refs := SimplifyPropertyReferences(instanceNode)
	if refs["visible"] != "On Sale" || refs["text"] != "Label" {
		t.Fatalf("unexpected simplified property references: %+v", refs)
	}

	compSetNode := map[string]any{
		"componentPropertyDefinitions": map[string]any{
			"Label#12:0":   map[string]any{"type": "TEXT", "defaultValue": "Click"},
			"On Sale#12:1": map[string]any{"type": "BOOLEAN", "defaultValue": false},
		},
	}
	defs := SimplifyPropertyDefinitions(compSetNode)
	if len(defs) != 2 {
		t.Fatalf("unexpected property definitions: %+v", defs)
	}
}

func TestCSSGridAndTextStyles(t *testing.T) {
	// 1. CSS Grid container + packed vs non-packed children + overlapping AABB zIndex
	gridParent := map[string]any{
		"id":            "10:0",
		"type":          "FRAME",
		"layoutMode":    "GRID",
		"gridRowGap":    8.0,
		"gridColumnGap": 12.0,
		"gridColumnsSizing": []any{
			map[string]any{"type": "FLEX", "value": 1.0},
			map[string]any{"type": "FIXED", "value": 120.0},
		},
		"gridRowsSizing": []any{
			map[string]any{"type": "HUG"},
		},
		"children": []any{
			map[string]any{
				"id":                       "10:2",
				"type":                     "FRAME",
				"gridRowAnchorIndex":       0.0,
				"gridColumnAnchorIndex":    1.0,
				"gridColumnSpan":           2.0,
				"gridChildHorizontalAlign": "CENTER",
				"gridChildVerticalAlign":   "MAX",
				"absoluteBoundingBox":      map[string]any{"x": 20.0, "y": 0.0, "width": 100.0, "height": 50.0},
			},
			map[string]any{
				"id":                    "10:1",
				"type":                  "FRAME",
				"gridRowAnchorIndex":    0.0,
				"gridColumnAnchorIndex": 0.0,
				"absoluteBoundingBox":   map[string]any{"x": 0.0, "y": 0.0, "width": 60.0, "height": 50.0}, // overlaps 10:2 (x=20..120)
			},
		},
	}

	reordered := ComputeGridChildOrder(gridParent)
	if len(reordered) != 2 || reordered[0]["id"] != "10:1" || reordered[1]["id"] != "10:2" {
		t.Fatalf("expected grid children reordered to 10:1 then 10:2, got %+v", reordered)
	}

	parentLayout := BuildSimplifiedLayout(gridParent, nil)
	parentMap := parentLayout.ToMap()
	if parentMap["mode"] != "grid" {
		t.Fatalf("expected mode=grid, got %+v", parentMap)
	}

	childLayout := BuildSimplifiedLayout(reordered[1], gridParent)
	childMap := childLayout.ToMap()
	if childMap["justifySelf"] != "center" || childMap["alignSelf"] != "end" || childMap["zIndex"] == nil {
		t.Fatalf("expected grid child alignment and zIndex on overlap, got %+v", childMap)
	}

	// 2. ExtractTextStyle + Ordered/Unordered plain list formatting + StableStringify
	textNode := map[string]any{
		"type":       "TEXT",
		"characters": "First item\nSecond item\nBullet item",
		"lineTypes":  []any{"ORDERED", "ORDERED", "UNORDERED"},
		"lineIndentations": []any{0.0, 0.0, 1.0},
		"style": map[string]any{
			"fontFamily":          "Inter",
			"fontStyle":           "Regular",
			"fontWeight":          500.0,
			"fontSize":            16.0,
			"lineHeightUnit":      "PIXELS",
			"lineHeightPx":        24.0,
			"letterSpacing":       0.8,
			"textCase":            "UPPER",
			"textAlignHorizontal": "CENTER",
			"textAlignVertical":   "CENTER",
			"italic":              true,
			"textDecoration":      "UNDERLINE",
			"paragraphSpacing":    8.0,
			"paragraphIndent":     4.0,
			"listSpacing":         6.0,
			"hyperlink":           map[string]any{"type": "URL", "url": "https://figma.com"},
			"opentypeFlags":       map[string]any{"liga": 1.0},
		},
	}
	ts := ExtractTextStyle(textNode)
	if ts["lineHeight"] != "24px" || ts["letterSpacing"] != "0.05em" || ts["textCase"] != "UPPER" || ts["italic"] != true {
		t.Fatalf("unexpected extracted text style: %+v", ts)
	}
	plainFormatted, _ := BuildFormattedText(textNode, nil)
	if plainFormatted != `1. First item\n2. Second item\n  - Bullet item` {
		t.Fatalf("unexpected plain list formatted text: %s", plainFormatted)
	}
	if s1, s2 := StableStringify(map[string]any{"b": 2, "a": 1}), `{"a":1,"b":2}`; s1 != s2 {
		t.Fatalf("expected %s, got %s", s2, s1)
	}
}

func TestRichTextFormatting(t *testing.T) {
	node := map[string]any{
		"type":       "TEXT",
		"characters": "Save 50% now",
		"style": map[string]any{
			"fontFamily": "Inter",
			"fontWeight": 400.0,
			"fontSize":   16.0,
		},
		"characterStyleOverrides": []any{0.0, 0.0, 0.0, 0.0, 0.0, 1.0, 1.0, 1.0, 0.0, 0.0, 0.0, 0.0},
		"styleOverrideTable": map[string]any{
			"1": map[string]any{
				"fontWeight": 700.0,
				"fills": []any{
					map[string]any{
						"type":  "SOLID",
						"color": map[string]any{"r": 1.0, "g": 0.0, "b": 0.0, "a": 1.0},
					},
				},
			},
		},
	}

	var registeredDelta map[string]any
	formatted, boldW := BuildFormattedText(node, func(delta map[string]any) string {
		registeredDelta = delta
		return "ts1"
	})

	if boldW != 700 {
		t.Fatalf("expected boldWeight 700, got %d", boldW)
	}
	if formatted != `Save {ts1}**50\%**{/ts1} now` && formatted != `Save {ts1}**50%**{/ts1} now` {
		t.Fatalf("unexpected formatted text: %s", formatted)
	}
	if registeredDelta == nil || registeredDelta["fills"] == nil {
		t.Fatalf("expected inline fills delta to be registered, got %+v", registeredDelta)
	}
}

func TestLayoutAndEffects(t *testing.T) {
	rootNode := map[string]any{
		"id":                     "1:1",
		"type":                   "FRAME",
		"clipsContent":           true,
		"layoutMode":             "HORIZONTAL",
		"primaryAxisAlignItems":  "SPACE_BETWEEN",
		"counterAxisAlignItems":  "CENTER",
		"itemSpacing":            99.0, // ignored when SPACE_BETWEEN
		"paddingTop":             16.0,
		"paddingRight":           24.0,
		"paddingBottom":          16.0,
		"paddingLeft":            24.0,
		"layoutSizingHorizontal": "FIXED",
		"layoutSizingVertical":   "HUG",
		"absoluteBoundingBox":    map[string]any{"x": 0.0, "y": 0.0, "width": 1440.0, "height": 80.0},
	}

	layout := BuildSimplifiedLayout(rootNode, nil)
	if layout.Mode != "row" || layout.JustifyContent != "space-between" || layout.AlignItems != "center" {
		t.Fatalf("unexpected flex layout: %+v", layout)
	}
	if layout.Gap != "" {
		t.Fatalf("expected empty gap when SPACE_BETWEEN, got %q", layout.Gap)
	}
	if layout.Padding != "16px 24px" {
		t.Fatalf("expected padding 16px 24px, got %q", layout.Padding)
	}
	if layout.Sizing["horizontal"] != "contextual" || layout.DesignedWidth != "1440px" {
		t.Fatalf("expected root contextual horizontal sizing and designedWidth=1440px, got %+v", layout)
	}

	effNode := map[string]any{
		"type": "TEXT",
		"effects": []any{
			map[string]any{
				"type":    "DROP_SHADOW",
				"visible": true,
				"offset":  map[string]any{"x": 0.0, "y": 4.0},
				"radius":  8.0,
				"spread":  0.0,
				"color":   map[string]any{"r": 0.0, "g": 0.0, "b": 0.0, "a": 0.25},
			},
			map[string]any{
				"type":    "LAYER_BLUR",
				"visible": true,
				"radius":  16.0,
			},
		},
	}
	eff := BuildSimplifiedEffects(effNode)
	if !strings.Contains(eff.TextShadow, "0px 4px 8px 0px rgba(0, 0, 0, 0.25)") || eff.Filter != "blur(8px)" {
		t.Fatalf("unexpected effects output: %+v", eff)
	}
}
