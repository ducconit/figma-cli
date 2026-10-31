package stitch

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/ducconit/figma-cli/internal/extractors"
	"github.com/ducconit/figma-cli/internal/figma"
	"github.com/ducconit/figma-cli/internal/tokens"
)

var (
	colorHexRegex   = regexp.MustCompile(`(?i)^#([0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$`)
	nonAlphaSlugRe  = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	pxDimensionRe   = regexp.MustCompile(`^([0-9.]+)(px)?$`)
)

// ExtractDesignSystem orchestrates dual-source extraction from Figma Variables and Canvas AST.
func ExtractDesignSystem(
	ctx context.Context,
	client *figma.Client,
	fileKey, nodeID string,
	opts ExtractionOptions,
) (*DesignSystemSpec, error) {
	if fileKey == "" {
		return nil, fmt.Errorf("fileKey is required to extract design system")
	}

	var varsMeta *figma.VariablesMeta
	if !opts.ForceCanvas {
		meta, err := client.GetLocalVariables(ctx, fileKey)
		if err == nil && meta != nil && len(meta.Variables) > 0 {
			varsMeta = meta
		}
	}

	depth := opts.Depth
	if depth <= 0 {
		depth = 5
	}

	var (
		rawData map[string]any
		err     error
	)
	if nodeID != "" {
		rawData, _, err = client.GetRawNode(ctx, fileKey, nodeID, depth)
	} else {
		rawData, _, err = client.GetRawFile(ctx, fileKey, depth)
	}
	if err != nil {
		// If raw node/file fetch fails but we have variables, still construct from variables
		if varsMeta != nil {
			spec := &DesignSystemSpec{
				Version:    "alpha",
				Name:       fileKey,
				Colors:     make(map[string]string),
				Typography: make(map[string]TypographyToken),
				Spacing:    make(map[string]string),
				Rounded:    make(map[string]string),
				Elevation:  make(map[string]string),
				Components: make(map[string]map[string]string),
			}
			populateFromVariables(spec, varsMeta)
			ensureHarmonicScalesAndFallbacks(spec)
			validateAndFixReferences(spec)
			return spec, nil
		}
		return nil, fmt.Errorf("fetch design data for file %s: %w", fileKey, err)
	}

	simplified, err := extractors.SimplifyRawFigmaObject(rawData, extractors.Options{
		MaxDepth: depth,
	})
	if err != nil {
		return nil, fmt.Errorf("simplify design tree: %w", err)
	}

	spec := ExtractFromSimplifiedDesign(simplified, varsMeta, opts)
	return spec, nil
}

// ExtractFromSimplifiedDesign extracts design tokens from SimplifiedDesign AST and optional VariablesMeta.
func ExtractFromSimplifiedDesign(
	design *extractors.SimplifiedDesign,
	varsMeta *figma.VariablesMeta,
	opts ExtractionOptions,
) *DesignSystemSpec {
	name := "Design System"
	if design != nil && design.Name != "" {
		name = design.Name
	}

	spec := &DesignSystemSpec{
		Version:     "alpha",
		Name:        cleanDesignName(name),
		Description: fmt.Sprintf("Design system specification for %s, adhering to the Google Stitch DESIGN.md open specification.", cleanDesignName(name)),
		Colors:      make(map[string]string),
		Typography:  make(map[string]TypographyToken),
		Spacing:     make(map[string]string),
		Rounded:     make(map[string]string),
		Elevation:   make(map[string]string),
		Components:  make(map[string]map[string]string),
	}

	// 1. Extract from Figma Variables if present
	if varsMeta != nil && len(varsMeta.Variables) > 0 {
		populateFromVariables(spec, varsMeta)
	}

	// 2. Extract from GlobalVars.Styles (Figma Paint & Text Styles)
	if design != nil {
		populateFromGlobalStyles(spec, design.GlobalVars.Styles)
	}

	// 3. Canvas Node Heuristic Extraction (frames, color swatches, typography samples, components)
	if design != nil {
		for _, node := range design.Nodes {
			extractFromNodeHierarchy(spec, node)
		}
		// Also scan component sets and definitions
		extractComponentTokens(spec, design)
	}

	// 4. Fill in missing harmonic scales & semantic fallbacks
	ensureHarmonicScalesAndFallbacks(spec)

	// 5. Validate and guarantee that all {path.to.token} references resolve
	validateAndFixReferences(spec)

	return spec
}

func cleanDesignName(name string) string {
	name = strings.TrimSpace(name)
	// Remove common community suffixes like (React, Vue, ...) or (Community)
	idx := strings.Index(name, "(")
	if idx > 3 {
		name = strings.TrimSpace(name[:idx])
	}
	name = strings.TrimRight(name, " -–—")
	if name == "" {
		return "Design System"
	}
	return name
}

func populateFromVariables(spec *DesignSystemSpec, meta *figma.VariablesMeta) {
	dt, err := tokens.ProcessVariables(meta)
	if err != nil || dt == nil {
		return
	}

	for _, col := range dt.Collections {
		defaultMode := col.DefaultMode
		for _, tok := range col.Tokens {
			val := tok.Values[defaultMode]
			if val == "" {
				continue
			}

			slug := normalizeTokenKey(tok.Name)
			switch tok.Type {
			case "COLOR":
				if isColorValue(val) {
					spec.Colors[slug] = val
				}
			case "FLOAT":
				lower := strings.ToLower(tok.Name)
				if strings.Contains(lower, "radius") || strings.Contains(lower, "corner") || strings.Contains(lower, "round") {
					spec.Rounded[slug] = val
				} else if strings.Contains(lower, "space") || strings.Contains(lower, "spacing") ||
					strings.Contains(lower, "gap") || strings.Contains(lower, "padding") || strings.Contains(lower, "margin") {
					spec.Spacing[slug] = val
				}
			}
		}
	}
}

func populateFromGlobalStyles(spec *DesignSystemSpec, styles map[string]any) {
	if len(styles) == 0 {
		return
	}

	for styleName, def := range styles {
		m, ok := def.(map[string]any)
		if !ok {
			continue
		}

		// Text style?
		if ff, hasFF := m["fontFamily"].(string); hasFF && ff != "" {
			tok := parseTypographyToken(m)
			tokenKey := normalizeTypographyKey(styleName)
			if tokenKey != "" {
				spec.Typography[tokenKey] = tok
			}
		}

		// Color fill style?
		if fills, ok := m["fills"].([]any); ok && len(fills) > 0 {
			if colorStr, ok := fills[0].(string); ok && isColorValue(colorStr) {
				key := normalizeTokenKey(styleName)
				if _, exists := spec.Colors[key]; !exists {
					spec.Colors[key] = colorStr
				}
			}
		}
	}
}

func parseTypographyToken(m map[string]any) TypographyToken {
	ff, _ := m["fontFamily"].(string)
	if ff == "" {
		ff = "Inter"
	}

	fsStr := "16px"
	switch v := m["fontSize"].(type) {
	case float64:
		fsStr = fmt.Sprintf("%gpx", math.Round(v*100)/100)
	case int:
		fsStr = fmt.Sprintf("%dpx", v)
	case string:
		fsStr = v
	}

	fw := 400
	switch v := m["fontWeight"].(type) {
	case int:
		fw = v
	case float64:
		fw = int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			fw = n
		}
	}

	lhStr := "24px"
	if lh, ok := m["lineHeight"].(string); ok && lh != "" {
		lhStr = lh
	} else if lhNum, ok := m["lineHeight"].(float64); ok && lhNum > 0 {
		lhStr = fmt.Sprintf("%gpx", math.Round(lhNum*100)/100)
	}

	tok := TypographyToken{
		FontFamily: ff,
		FontSize:   fsStr,
		FontWeight: fw,
		LineHeight: lhStr,
	}

	if ls, ok := m["letterSpacing"].(string); ok && ls != "" {
		tok.LetterSpacing = ls
	}
	return tok
}

func extractFromNodeHierarchy(spec *DesignSystemSpec, node *extractors.SimplifiedNode) {
	if node == nil {
		return
	}

	// A. Check for Color Swatch Cards
	if isColorContainer(node.Name) {
		extractColorsFromSwatchContainer(spec, node)
	}

	// B. Check for Typography Sample Frames
	if isTypographyContainer(node.Name) {
		extractTypographyFromContainer(spec, node)
	}

	// C. Check node itself for fills & border radius
	if node.BorderRadius != "" && node.BorderRadius != "0px" {
		recordRadius(spec, node.BorderRadius)
	}

	for _, child := range node.Children {
		extractFromNodeHierarchy(spec, child)
	}
}

func isColorContainer(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "color") || strings.Contains(l, "palette") ||
		strings.Contains(l, "swatch") || strings.Contains(l, "shade") ||
		strings.Contains(l, "grayscale") || strings.Contains(l, "neutral") ||
		strings.Contains(l, "primary") || strings.Contains(l, "secondary")
}

func isTypographyContainer(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "typography") || strings.Contains(l, "headings") ||
		strings.Contains(l, "display") || strings.Contains(l, "body text") ||
		strings.Contains(l, "type scale") || strings.Contains(l, "font")
}

func extractColorsFromSwatchContainer(spec *DesignSystemSpec, node *extractors.SimplifiedNode) {
	// Pattern 1: Frame name is "Category/ColorName" or "Primary / 500" with a fill
	if fillsStr, ok := node.Fills.(string); ok && isColorValue(fillsStr) {
		slug := normalizeTokenKey(node.Name)
		if slug != "" && !strings.HasPrefix(slug, "frame") && !strings.HasPrefix(slug, "group") {
			if _, exists := spec.Colors[slug]; !exists {
				spec.Colors[slug] = fillsStr
			}
		}
	} else if fillsList, ok := node.Fills.([]any); ok && len(fillsList) > 0 {
		if c, ok := fillsList[0].(string); ok && isColorValue(c) {
			slug := normalizeTokenKey(node.Name)
			if slug != "" && !strings.HasPrefix(slug, "frame") && !strings.HasPrefix(slug, "group") {
				if _, exists := spec.Colors[slug]; !exists {
					spec.Colors[slug] = c
				}
			}
		}
	}

	// Pattern 2: Swatch card with child shape (fill) and child text (name / hex)
	var (
		foundColor string
		label      string
	)

	for _, child := range node.Children {
		// Discover fill color from shape
		if foundColor == "" {
			if s, ok := child.Fills.(string); ok && isColorValue(s) {
				foundColor = s
			} else if list, ok := child.Fills.([]any); ok && len(list) > 0 {
				if c, ok := list[0].(string); ok && isColorValue(c) {
					foundColor = c
				}
			}
		}

		// Discover label from TEXT node
		if child.Type == "TEXT" && child.Text != "" {
			trimmed := strings.TrimSpace(child.Text)
			if !isColorValue(trimmed) && len(trimmed) < 40 && label == "" {
				label = trimmed
			}
		}
	}

	if foundColor != "" {
		tokenName := label
		if tokenName == "" {
			tokenName = node.Name
		}
		slug := normalizeTokenKey(tokenName)
		if slug != "" && !strings.HasPrefix(slug, "frame") && !strings.HasPrefix(slug, "group") {
			if _, exists := spec.Colors[slug]; !exists {
				spec.Colors[slug] = foundColor
			}
		}
	}
}

func extractTypographyFromContainer(spec *DesignSystemSpec, node *extractors.SimplifiedNode) {
	if node.Type == "TEXT" && node.TextStyle != nil {
		if m, ok := node.TextStyle.(map[string]any); ok {
			tok := parseTypographyToken(m)
			key := normalizeTypographyKey(node.Name)
			if key == "" || strings.HasPrefix(key, "text") {
				key = normalizeTypographyKey(node.Text)
			}
			if key != "" && !strings.HasPrefix(key, "text") {
				spec.Typography[key] = tok
			}
		}
	}
}

func extractComponentTokens(spec *DesignSystemSpec, design *extractors.SimplifiedDesign) {
	if design == nil {
		return
	}

	// 1. Scan Components map
	for _, comp := range design.Components {
		registerComponentVariant(spec, comp.Name)
	}

	// 2. Scan ComponentSets map
	for _, set := range design.ComponentSets {
		registerComponentVariant(spec, set.Name)
	}

	// 3. Scan Node tree for COMPONENT and COMPONENT_SET nodes
	var walkComp func(n *extractors.SimplifiedNode)
	walkComp = func(n *extractors.SimplifiedNode) {
		if n == nil {
			return
		}
		if n.Type == "COMPONENT" || n.Type == "COMPONENT_SET" {
			registerComponentVariant(spec, n.Name)
			if n.BorderRadius != "" {
				recordRadius(spec, n.BorderRadius)
			}
		}
		for _, c := range n.Children {
			walkComp(c)
		}
	}

	for _, root := range design.Nodes {
		walkComp(root)
	}

	// Ensure core components always have rich token specs
	ensureStandardComponents(spec)
}

func registerComponentVariant(spec *DesignSystemSpec, rawName string) {
	lower := strings.ToLower(rawName)
	if strings.Contains(lower, "button") {
		if _, exists := spec.Components["button-primary"]; !exists {
			spec.Components["button-primary"] = map[string]string{
				"backgroundColor": "{colors.primary}",
				"textColor":       "{colors.white}",
				"rounded":         "{rounded.sm}",
				"padding":         "8px 16px",
				"typography":      "{typography.body-lg}",
			}
		}
		if strings.Contains(lower, "secondary") {
			spec.Components["button-secondary"] = map[string]string{
				"backgroundColor": "{colors.secondary}",
				"textColor":       "{colors.white}",
				"rounded":         "{rounded.sm}",
				"padding":         "8px 16px",
				"typography":      "{typography.body-lg}",
			}
		}
		if strings.Contains(lower, "outline") {
			spec.Components["button-outline"] = map[string]string{
				"backgroundColor": "transparent",
				"textColor":       "{colors.primary}",
				"rounded":         "{rounded.sm}",
				"padding":         "8px 16px",
				"typography":      "{typography.body-lg}",
			}
		}
	} else if strings.Contains(lower, "input") || strings.Contains(lower, "text field") {
		spec.Components["input-default"] = map[string]string{
			"backgroundColor": "{colors.surface}",
			"textColor":       "{colors.neutral}",
			"rounded":         "{rounded.sm}",
			"padding":         "8px 16px",
			"height":          "38px",
			"typography":      "{typography.body-lg}",
		}
	} else if strings.Contains(lower, "badge") || strings.Contains(lower, "chip") || strings.Contains(lower, "tag") {
		spec.Components["badge-success"] = map[string]string{
			"backgroundColor": "{colors.tint-success}",
			"textColor":       "{colors.success}",
			"rounded":         "{rounded.sm}",
			"padding":         "4px 8px",
			"typography":      "{typography.body-xs}",
		}
	} else if strings.Contains(lower, "card") {
		spec.Components["card"] = map[string]string{
			"backgroundColor": "{colors.surface}",
			"rounded":         "{rounded.sm}",
			"padding":         "24px",
		}
	} else if strings.Contains(lower, "avatar") {
		spec.Components["avatar-circle"] = map[string]string{
			"rounded": "{rounded.full}",
			"size":    "48px",
		}
	}
}

func ensureStandardComponents(spec *DesignSystemSpec) {
	if _, ok := spec.Components["button-primary"]; !ok {
		spec.Components["button-primary"] = map[string]string{
			"backgroundColor": "{colors.primary}",
			"textColor":       "{colors.white}",
			"rounded":         "{rounded.sm}",
			"padding":         "8px 16px",
			"typography":      "{typography.body-lg}",
		}
	}
	if _, ok := spec.Components["button-secondary"]; !ok {
		spec.Components["button-secondary"] = map[string]string{
			"backgroundColor": "{colors.secondary}",
			"textColor":       "{colors.white}",
			"rounded":         "{rounded.sm}",
			"padding":         "8px 16px",
			"typography":      "{typography.body-lg}",
		}
	}
	if _, ok := spec.Components["input-default"]; !ok {
		spec.Components["input-default"] = map[string]string{
			"backgroundColor": "{colors.surface}",
			"textColor":       "{colors.neutral}",
			"rounded":         "{rounded.sm}",
			"padding":         "8px 16px",
			"height":          "38px",
			"typography":      "{typography.body-lg}",
		}
	}
	if _, ok := spec.Components["card"]; !ok {
		spec.Components["card"] = map[string]string{
			"backgroundColor": "{colors.surface}",
			"rounded":         "{rounded.sm}",
			"padding":         "24px",
		}
	}
	if _, ok := spec.Components["badge-success"]; !ok {
		spec.Components["badge-success"] = map[string]string{
			"backgroundColor": "{colors.tint-success}",
			"textColor":       "{colors.success}",
			"rounded":         "{rounded.sm}",
			"padding":         "4px 8px",
			"typography":      "{typography.body-xs}",
		}
	}
	if _, ok := spec.Components["avatar-circle"]; !ok {
		spec.Components["avatar-circle"] = map[string]string{
			"rounded": "{rounded.full}",
			"size":    "48px",
		}
	}
}

func recordRadius(spec *DesignSystemSpec, radiusStr string) {
	radiusStr = strings.TrimSpace(radiusStr)
	if radiusStr == "" || radiusStr == "0px" {
		return
	}

	// If numeric, assign to sm, md, lg, or full
	var num float64
	if matches := pxDimensionRe.FindStringSubmatch(radiusStr); len(matches) > 1 {
		num, _ = strconv.ParseFloat(matches[1], 64)
	}

	if num >= 999 {
		spec.Rounded["full"] = "9999px"
	} else if num > 0 && num <= 6 {
		if _, exists := spec.Rounded["sm"]; !exists {
			spec.Rounded["sm"] = radiusStr
		}
	} else if num > 6 && num <= 10 {
		if _, exists := spec.Rounded["md"]; !exists {
			spec.Rounded["md"] = radiusStr
		}
	} else if num > 10 {
		if _, exists := spec.Rounded["lg"]; !exists {
			spec.Rounded["lg"] = radiusStr
		}
	}
}

func ensureHarmonicScalesAndFallbacks(spec *DesignSystemSpec) {
	// 1. Spacing scale
	if len(spec.Spacing) < 4 {
		standardSpacing := map[string]string{
			"xs":  "4px",
			"sm":  "8px",
			"md":  "12px",
			"lg":  "16px",
			"xl":  "20px",
			"2xl": "24px",
			"3xl": "32px",
			"4xl": "48px",
			"5xl": "64px",
		}
		for k, v := range standardSpacing {
			if _, exists := spec.Spacing[k]; !exists {
				spec.Spacing[k] = v
			}
		}
	}

	// 2. Rounded scale
	if _, ok := spec.Rounded["none"]; !ok {
		spec.Rounded["none"] = "0px"
	}
	if _, ok := spec.Rounded["sm"]; !ok {
		spec.Rounded["sm"] = "4px"
	}
	if _, ok := spec.Rounded["md"]; !ok {
		spec.Rounded["md"] = "6px"
	}
	if _, ok := spec.Rounded["lg"]; !ok {
		spec.Rounded["lg"] = "8px"
	}
	if _, ok := spec.Rounded["full"]; !ok {
		spec.Rounded["full"] = "9999px"
	}

	// 3. Essential semantic colors
	colorDefaults := map[string]string{
		"primary":      "#2563EB",
		"secondary":    "#0F172A",
		"white":        "#FFFFFF",
		"neutral":      "#0F172A",
		"surface":      "#FFFFFF",
		"border":       "#CBD5E1",
		"success":      "#059669",
		"warning":      "#D97706",
		"danger":       "#DC2626",
		"error":        "#DC2626",
		"info":         "#0284C7",
		"tint-success": "#ECFDF5",
		"tint-warning": "#FFFBEB",
		"tint-danger":  "#FEF2F2",
		"tint-info":    "#F0F9FF",
	}
	for k, v := range colorDefaults {
		if _, exists := spec.Colors[k]; !exists {
			spec.Colors[k] = v
		}
	}

	// 4. Essential typography tokens
	if len(spec.Typography) < 3 {
		fontFamily := "Inter"
		// If another typography token has fontFamily, reuse it
		for _, t := range spec.Typography {
			if t.FontFamily != "" {
				fontFamily = t.FontFamily
				break
			}
		}
		standardTypo := map[string]TypographyToken{
			"heading-1": {FontFamily: fontFamily, FontSize: "28px", FontWeight: 700, LineHeight: "42px"},
			"heading-2": {FontFamily: fontFamily, FontSize: "24px", FontWeight: 700, LineHeight: "36px"},
			"heading-3": {FontFamily: fontFamily, FontSize: "20px", FontWeight: 700, LineHeight: "30px"},
			"body-lg":   {FontFamily: fontFamily, FontSize: "16px", FontWeight: 500, LineHeight: "24px"},
			"body-md":   {FontFamily: fontFamily, FontSize: "14px", FontWeight: 400, LineHeight: "21px"},
			"body-sm":   {FontFamily: fontFamily, FontSize: "12px", FontWeight: 400, LineHeight: "18px"},
			"body-xs":   {FontFamily: fontFamily, FontSize: "10px", FontWeight: 600, LineHeight: "15px"},
		}
		for k, v := range standardTypo {
			if _, exists := spec.Typography[k]; !exists {
				spec.Typography[k] = v
			}
		}
	}
}

// ValidateTokens checks and resolves token references, returning any warnings or corrections applied.
func ValidateTokens(spec *DesignSystemSpec) []string {
	return validateAndFixReferences(spec)
}

func validateAndFixReferences(spec *DesignSystemSpec) []string {
	var warnings []string
	for compName, props := range spec.Components {
		for propKey, propVal := range props {
			if strings.HasPrefix(propVal, "{") && strings.HasSuffix(propVal, "}") {
				refPath := strings.Trim(propVal, "{}")
				parts := strings.Split(refPath, ".")
				if len(parts) >= 2 {
					group := parts[0]
					token := strings.Join(parts[1:], ".")

					switch group {
					case "colors":
						if _, exists := spec.Colors[token]; !exists {
							// Check if token exists with different hyphenation
							tokenSlug := normalizeTokenKey(token)
							if _, ok := spec.Colors[tokenSlug]; ok {
								props[propKey] = fmt.Sprintf("{colors.%s}", tokenSlug)
								warnings = append(warnings, fmt.Sprintf("%s.%s: normalized token ref from %s to %s", compName, propKey, token, tokenSlug))
							} else if _, ok := spec.Colors["primary"]; ok {
								props[propKey] = "{colors.primary}"
								warnings = append(warnings, fmt.Sprintf("%s.%s: unknown color %s resolved to {colors.primary}", compName, propKey, token))
							}
						}
					case "rounded":
						if _, exists := spec.Rounded[token]; !exists {
							if token == "default" {
								props[propKey] = "{rounded.sm}"
								warnings = append(warnings, fmt.Sprintf("%s.%s: radius default resolved to {rounded.sm}", compName, propKey))
							} else if _, ok := spec.Rounded["sm"]; ok {
								props[propKey] = "{rounded.sm}"
								warnings = append(warnings, fmt.Sprintf("%s.%s: unknown radius %s resolved to {rounded.sm}", compName, propKey, token))
							}
						}
					case "typography":
						if _, exists := spec.Typography[token]; !exists {
							matched := ""
							sortedKeys := sortTypographyKeys(spec.Typography)
							for _, k := range sortedKeys {
								if strings.Contains(k, token) || strings.Contains(token, k) {
									matched = k
									break
								}
							}
							if matched != "" {
								props[propKey] = fmt.Sprintf("{typography.%s}", matched)
								warnings = append(warnings, fmt.Sprintf("%s.%s: typography %s resolved to {typography.%s}", compName, propKey, token, matched))
							} else if _, ok := spec.Typography["body-lg"]; ok {
								props[propKey] = "{typography.body-lg}"
								warnings = append(warnings, fmt.Sprintf("%s.%s: unknown typography %s resolved to {typography.body-lg}", compName, propKey, token))
							} else if _, ok := spec.Typography["body-md"]; ok {
								props[propKey] = "{typography.body-md}"
								warnings = append(warnings, fmt.Sprintf("%s.%s: unknown typography %s resolved to {typography.body-md}", compName, propKey, token))
							} else if len(sortedKeys) > 0 {
								firstKey := sortedKeys[0]
								props[propKey] = fmt.Sprintf("{typography.%s}", firstKey)
								warnings = append(warnings, fmt.Sprintf("%s.%s: unknown typography %s resolved to {typography.%s}", compName, propKey, token, firstKey))
							}
						}
					}
				}
			}
		}
		spec.Components[compName] = props
	}
	return warnings
}

func normalizeTokenKey(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}

	// Remove common prefixes
	prefixes := []string{
		"Colors/", "Color/", "System Color/", "Palette/", "Theme/",
		"Typography/", "Type/", "Headings/", "Display/", "Body/",
		"Spacing/", "Space/", "Radius/", "Rounded/", "Border Radius/",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(strings.ToLower(cleaned), strings.ToLower(p)) {
			cleaned = cleaned[len(p):]
			break
		}
	}

	parts := strings.FieldsFunc(cleaned, func(r rune) bool {
		return r == '/' || r == '_' || r == ' ' || r == '-' || r == '.'
	})

	var slugParts []string
	for _, p := range parts {
		sub := nonAlphaSlugRe.ReplaceAllString(p, "")
		if sub != "" {
			slugParts = append(slugParts, strings.ToLower(sub))
		}
	}

	if len(slugParts) == 0 {
		return ""
	}
	return strings.Join(slugParts, "-")
}

func normalizeTypographyKey(raw string) string {
	cleaned := normalizeTokenKey(raw)
	if cleaned == "" {
		return ""
	}

	lower := strings.ToLower(cleaned)

	// Direct semantic match
	if strings.Contains(lower, "display-1") || strings.Contains(lower, "display1") {
		return "display-1"
	}
	if strings.Contains(lower, "display-2") || strings.Contains(lower, "display2") {
		return "display-2"
	}
	if strings.Contains(lower, "display-3") || strings.Contains(lower, "display3") {
		return "display-3"
	}
	if strings.Contains(lower, "heading-1") || strings.Contains(lower, "heading1") || lower == "h1" {
		return "heading-1"
	}
	if strings.Contains(lower, "heading-2") || strings.Contains(lower, "heading2") || lower == "h2" {
		return "heading-2"
	}
	if strings.Contains(lower, "heading-3") || strings.Contains(lower, "heading3") || lower == "h3" {
		return "heading-3"
	}
	if strings.Contains(lower, "heading-4") || strings.Contains(lower, "heading4") || lower == "h4" {
		return "heading-4"
	}
	if strings.Contains(lower, "heading-5") || strings.Contains(lower, "heading5") || lower == "h5" {
		return "heading-5"
	}
	if strings.Contains(lower, "heading-6") || strings.Contains(lower, "heading6") || lower == "h6" {
		return "heading-6"
	}
	if strings.Contains(lower, "body-xl") || strings.Contains(lower, "extra-large") {
		return "body-xl"
	}
	if strings.Contains(lower, "body-lg") || strings.Contains(lower, "body-large") {
		return "body-lg"
	}
	if strings.Contains(lower, "body-md") || strings.Contains(lower, "body-medium") {
		return "body-md"
	}
	if strings.Contains(lower, "body-sm") || strings.Contains(lower, "body-small") {
		return "body-sm"
	}
	if strings.Contains(lower, "body-xs") || strings.Contains(lower, "extra-small") {
		return "body-xs"
	}

	return cleaned
}

func isColorValue(val string) bool {
	val = strings.TrimSpace(val)
	if colorHexRegex.MatchString(val) {
		return true
	}
	return strings.HasPrefix(val, "rgba(") || strings.HasPrefix(val, "rgb(") ||
		strings.HasPrefix(val, "hsl(") || strings.HasPrefix(val, "hsla(")
}
