package stitch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SerializeStitch formats DesignSystemSpec into a complete Google Stitch DESIGN.md document.
func SerializeStitch(spec *DesignSystemSpec) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("spec cannot be nil")
	}

	validateAndFixReferences(spec)

	var sb strings.Builder

	// 1. YAML Frontmatter
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("version: %s\n", defaultStr(spec.Version, "alpha")))
	sb.WriteString(fmt.Sprintf("name: %s\n", defaultStr(spec.Name, "Design System")))
	if spec.Description != "" {
		sb.WriteString(fmt.Sprintf("description: %s\n", spec.Description))
	}

	// Frontmatter: Colors
	if len(spec.Colors) > 0 {
		sb.WriteString("\ncolors:\n")
		sortedColors := sortColorKeys(spec.Colors)
		for _, k := range sortedColors {
			v := spec.Colors[k]
			sb.WriteString(fmt.Sprintf("  %s: %q\n", k, v))
		}
	}

	// Frontmatter: Typography
	if len(spec.Typography) > 0 {
		sb.WriteString("\ntypography:\n")
		sortedTypo := sortTypographyKeys(spec.Typography)
		for _, k := range sortedTypo {
			tok := spec.Typography[k]
			sb.WriteString(fmt.Sprintf("  %s:\n", k))
			sb.WriteString(fmt.Sprintf("    fontFamily: %s\n", tok.FontFamily))
			sb.WriteString(fmt.Sprintf("    fontSize: %s\n", tok.FontSize))
			sb.WriteString(fmt.Sprintf("    fontWeight: %d\n", tok.FontWeight))
			sb.WriteString(fmt.Sprintf("    lineHeight: %s\n", tok.LineHeight))
			if tok.LetterSpacing != "" {
				sb.WriteString(fmt.Sprintf("    letterSpacing: %s\n", tok.LetterSpacing))
			}
		}
	}

	// Frontmatter: Spacing
	if len(spec.Spacing) > 0 {
		sb.WriteString("\nspacing:\n")
		sortedSpacing := sortScaleKeys(spec.Spacing)
		for _, k := range sortedSpacing {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", k, spec.Spacing[k]))
		}
	}

	// Frontmatter: Rounded
	if len(spec.Rounded) > 0 {
		sb.WriteString("\nrounded:\n")
		sortedRounded := sortRoundedKeys(spec.Rounded)
		for _, k := range sortedRounded {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", k, spec.Rounded[k]))
		}
	}

	// Frontmatter: Elevation
	if len(spec.Elevation) > 0 {
		sb.WriteString("\nelevation:\n")
		elKeys := sortedMapKeys(spec.Elevation)
		for _, k := range elKeys {
			sb.WriteString(fmt.Sprintf("  %s: %q\n", k, spec.Elevation[k]))
		}
	}

	// Frontmatter: Components
	if len(spec.Components) > 0 {
		sb.WriteString("\ncomponents:\n")
		compKeys := sortedMapKeys(spec.Components)
		for _, comp := range compKeys {
			sb.WriteString(fmt.Sprintf("  %s:\n", comp))
			props := spec.Components[comp]
			propKeys := sortedMapKeys(props)
			for _, pk := range propKeys {
				val := props[pk]
				sb.WriteString(fmt.Sprintf("    %s: %q\n", pk, val))
			}
		}
	}

	sb.WriteString("---\n\n")

	// 2. Markdown Body
	sb.WriteString(fmt.Sprintf("# %s Design System Specification\n\n", spec.Name))
	sb.WriteString("This document defines the foundational design tokens, scales, and component guidelines adhering to the open [Google Stitch DESIGN.md](https://stitch.withgoogle.com/docs/design-md/specification/) standard.\n\n")

	// Markdown: Colors Table
	if len(spec.Colors) > 0 {
		sb.WriteString("## Color Tokens\n\n")
		sb.WriteString("| Token | Value | Role / Usage |\n")
		sb.WriteString("| :--- | :--- | :--- |\n")
		for _, k := range sortColorKeys(spec.Colors) {
			v := spec.Colors[k]
			role := guessColorRole(k)
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s |\n", k, v, role))
		}
		sb.WriteString("\n")
	}

	// Markdown: Typography Table
	if len(spec.Typography) > 0 {
		sb.WriteString("## Typography Scale\n\n")
		sb.WriteString("| Token | Font Family | Size | Weight | Line Height | Usage |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- |\n")
		for _, k := range sortTypographyKeys(spec.Typography) {
			tok := spec.Typography[k]
			usage := guessTypographyUsage(k)
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | `%d` | `%s` | %s |\n",
				k, tok.FontFamily, tok.FontSize, tok.FontWeight, tok.LineHeight, usage))
		}
		sb.WriteString("\n")
	}

	// Markdown: Spacing Table
	if len(spec.Spacing) > 0 {
		sb.WriteString("## Spacing Tokens\n\n")
		sb.WriteString("| Scale | Value | Equivalent rem |\n")
		sb.WriteString("| :--- | :--- | :--- |\n")
		for _, k := range sortScaleKeys(spec.Spacing) {
			v := spec.Spacing[k]
			rem := pxToRem(v)
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` |\n", k, v, rem))
		}
		sb.WriteString("\n")
	}

	// Markdown: Rounded Table
	if len(spec.Rounded) > 0 {
		sb.WriteString("## Border Radius Tokens\n\n")
		sb.WriteString("| Token | Value | Target Elements |\n")
		sb.WriteString("| :--- | :--- | :--- |\n")
		for _, k := range sortRoundedKeys(spec.Rounded) {
			v := spec.Rounded[k]
			target := guessRadiusTarget(k)
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s |\n", k, v, target))
		}
		sb.WriteString("\n")
	}

	// Markdown: Components
	if len(spec.Components) > 0 {
		sb.WriteString("## Component Token Specs\n\n")
		for _, comp := range sortedMapKeys(spec.Components) {
			sb.WriteString(fmt.Sprintf("### `%s`\n\n", comp))
			props := spec.Components[comp]
			for _, pk := range sortedMapKeys(props) {
				sb.WriteString(fmt.Sprintf("- **%s**: `%s`\n", pk, props[pk]))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String(), nil
}

// ToJSON exports DesignSystemSpec as indented JSON.
func (s *DesignSystemSpec) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal design system to JSON: %w", err)
	}
	return string(bytes), nil
}

func defaultStr(val, def string) string {
	if strings.TrimSpace(val) == "" {
		return def
	}
	return strings.TrimSpace(val)
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortColorKeys(m map[string]string) []string {
	corePriorities := map[string]int{
		"primary":        10,
		"secondary":      20,
		"dark":           30,
		"light":          40,
		"white":          50,
		"neutral":        60,
		"surface":        70,
		"surface-dim":    80,
		"border":         90,
		"divider":        100,
		"success":        110,
		"warning":        120,
		"danger":         130,
		"error":          140,
		"info":           150,
		"tint-primary":   200,
		"tint-secondary": 210,
		"tint-success":   220,
		"tint-warning":   230,
		"tint-danger":    240,
		"tint-info":      250,
	}

	keys := sortedMapKeys(m)
	sort.SliceStable(keys, func(i, j int) bool {
		p1, has1 := corePriorities[keys[i]]
		p2, has2 := corePriorities[keys[j]]
		if !has1 {
			p1 = 999
		}
		if !has2 {
			p2 = 999
		}
		if p1 != p2 {
			return p1 < p2
		}
		return keys[i] < keys[j]
	})
	return keys
}

func sortTypographyKeys(m map[string]TypographyToken) []string {
	typoOrder := map[string]int{
		"display-1": 10,
		"display-2": 20,
		"display-3": 30,
		"display-4": 40,
		"display-5": 50,
		"display-6": 60,
		"heading-1": 100,
		"heading-2": 110,
		"heading-3": 120,
		"heading-4": 130,
		"heading-5": 140,
		"heading-6": 150,
		"body-xl":   200,
		"body-lg":   210,
		"body-md":   220,
		"body-sm":   230,
		"body-xs":   240,
	}

	keys := sortedMapKeys(m)
	sort.SliceStable(keys, func(i, j int) bool {
		p1, has1 := typoOrder[keys[i]]
		p2, has2 := typoOrder[keys[j]]
		if !has1 {
			p1 = 999
		}
		if !has2 {
			p2 = 999
		}
		if p1 != p2 {
			return p1 < p2
		}
		return keys[i] < keys[j]
	})
	return keys
}

func sortScaleKeys(m map[string]string) []string {
	scaleOrder := map[string]int{
		"xs":  10,
		"sm":  20,
		"md":  30,
		"lg":  40,
		"xl":  50,
		"2xl": 60,
		"3xl": 70,
		"4xl": 80,
		"5xl": 90,
		"6xl": 100,
	}

	keys := sortedMapKeys(m)
	sort.SliceStable(keys, func(i, j int) bool {
		p1, has1 := scaleOrder[keys[i]]
		p2, has2 := scaleOrder[keys[j]]
		if !has1 {
			p1 = 999
		}
		if !has2 {
			p2 = 999
		}
		if p1 != p2 {
			return p1 < p2
		}
		return keys[i] < keys[j]
	})
	return keys
}

func sortRoundedKeys(m map[string]string) []string {
	roundedOrder := map[string]int{
		"none": 10,
		"xs":   20,
		"sm":   30,
		"md":   40,
		"lg":   50,
		"xl":   60,
		"2xl":  70,
		"full": 100,
	}

	keys := sortedMapKeys(m)
	sort.SliceStable(keys, func(i, j int) bool {
		p1, has1 := roundedOrder[keys[i]]
		p2, has2 := roundedOrder[keys[j]]
		if !has1 {
			p1 = 999
		}
		if !has2 {
			p2 = 999
		}
		if p1 != p2 {
			return p1 < p2
		}
		return keys[i] < keys[j]
	})
	return keys
}

func guessColorRole(token string) string {
	switch token {
	case "primary":
		return "Brand primary accent & call-to-actions"
	case "secondary":
		return "Brand secondary contrast & headers"
	case "white":
		return "Pure white surface / icon fill"
	case "surface":
		return "Card & panel backgrounds"
	case "border":
		return "Form field borders and table outlines"
	case "neutral":
		return "Primary body text color"
	case "success":
		return "Success badges, confirmations, positive states"
	case "warning":
		return "Warning alerts and pending indicators"
	case "danger", "error":
		return "Error messages and destructive actions"
	case "info":
		return "Informational banners and helpful hints"
	}
	if strings.HasPrefix(token, "tint-") {
		return "Subtle background for badges, chips, and toasts"
	}
	if strings.Contains(token, "grey") || strings.Contains(token, "gray") {
		return "Neutral tonal step for typography, borders, and fills"
	}
	return "Semantic palette token"
}

func guessTypographyUsage(token string) string {
	switch token {
	case "display-1", "display-2":
		return "Hero headers and high-impact banners"
	case "heading-1":
		return "Page main titles"
	case "heading-2":
		return "Section headers and dialog titles"
	case "heading-3", "heading-4":
		return "Card titles and subsection headers"
	case "heading-5", "heading-6":
		return "Widget headers and group labels"
	case "body-xl", "body-lg":
		return "Lead paragraphs and button labels"
	case "body-md":
		return "Default body text and form inputs"
	case "body-sm":
		return "Captions, table cells, and helper text"
	case "body-xs":
		return "Badges, chips, and legal footnotes"
	}
	return "Standard typography style"
}

func guessRadiusTarget(token string) string {
	switch token {
	case "none":
		return "Flat layouts, square containers, divider lines"
	case "sm":
		return "Buttons, form inputs, badges, select menus"
	case "md":
		return "Cards, modals, dropdown containers"
	case "lg":
		return "Large hero cards, banners, drawers"
	case "full":
		return "Circular avatars, pill badges, tag filters"
	}
	return "Rounded corner radius"
}

func pxToRem(pxStr string) string {
	pxStr = strings.TrimSpace(pxStr)
	numStr := strings.TrimSuffix(pxStr, "px")
	if n, err := strconv.ParseFloat(numStr, 64); err == nil && n > 0 {
		return fmt.Sprintf("%grem", n/16.0)
	}
	return pxStr
}
