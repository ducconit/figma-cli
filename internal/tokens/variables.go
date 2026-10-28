package tokens

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/ducconit/figma-cli/internal/figma"
	"github.com/ducconit/figma-cli/internal/transformers"
)

// FormatType defines output formats for tokens.
type FormatType string

const (
	FormatCSS      FormatType = "css"
	FormatTailwind FormatType = "tailwind"
	FormatJSON     FormatType = "json"
)

// ParseFormat validates and normalizes output format string.
func ParseFormat(raw string) (FormatType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "css", "":
		return FormatCSS, nil
	case "tailwind", "tw":
		return FormatTailwind, nil
	case "json":
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("unsupported token format %q (allowed: css, tailwind, json)", raw)
	}
}

// ResolvedToken represents a single resolved token across modes.
type ResolvedToken struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	CSSName     string            `json:"cssName"`
	Type        string            `json:"type"` // COLOR, FLOAT, STRING, BOOLEAN
	Description string            `json:"description,omitempty"`
	Values      map[string]string `json:"values"` // modeName -> formatted value string
}

// ResolvedCollection groups tokens belonging to a Figma variable collection.
type ResolvedCollection struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Modes       []string        `json:"modes"`
	DefaultMode string          `json:"defaultMode"`
	Tokens      []ResolvedToken `json:"tokens"`
}

// DesignTokens holds all collections and resolved tokens for a file.
type DesignTokens struct {
	Collections []ResolvedCollection `json:"collections"`
}

var nonAlphanumericSlugPattern = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// Slugify converts a variable name (e.g. "Colors/Primary/500") into a clean CSS variable name ("--colors-primary-500").
func Slugify(name string) string {
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		return "--token"
	}
	parts := strings.Split(cleaned, "/")
	cleanedParts := make([]string, 0, len(parts))
	for _, p := range parts {
		sub := nonAlphanumericSlugPattern.ReplaceAllString(strings.TrimSpace(p), "-")
		sub = strings.Trim(sub, "-")
		if sub != "" {
			cleanedParts = append(cleanedParts, strings.ToLower(sub))
		}
	}
	if len(cleanedParts) == 0 {
		return "--token"
	}
	return "--" + strings.Join(cleanedParts, "-")
}

// ProcessVariables parses Figma VariablesMeta, resolves aliases with cycle detection,
// and normalizes values across all modes into DesignTokens.
func ProcessVariables(meta *figma.VariablesMeta) (*DesignTokens, error) {
	if meta == nil || len(meta.Variables) == 0 {
		return &DesignTokens{Collections: []ResolvedCollection{}}, nil
	}

	// Sort collection IDs deterministically
	colIDs := make([]string, 0, len(meta.VariableCollections))
	for colID := range meta.VariableCollections {
		colIDs = append(colIDs, colID)
	}
	sort.Strings(colIDs)

	// Map modeID -> modeName for each collection
	type modeLookup struct {
		idToName    map[string]string
		nameToID    map[string]string
		defaultName string
		modeNames   []string
	}
	colLookups := make(map[string]modeLookup, len(colIDs))
	for _, colID := range colIDs {
		col := meta.VariableCollections[colID]
		lu := modeLookup{
			idToName:  make(map[string]string, len(col.Modes)),
			nameToID:  make(map[string]string, len(col.Modes)),
			modeNames: make([]string, 0, len(col.Modes)),
		}
		for _, m := range col.Modes {
			lu.idToName[m.ModeID] = m.Name
			lu.nameToID[strings.ToLower(m.Name)] = m.ModeID
			lu.modeNames = append(lu.modeNames, m.Name)
			if m.ModeID == col.DefaultModeID {
				lu.defaultName = m.Name
			}
		}
		if lu.defaultName == "" && len(col.Modes) > 0 {
			lu.defaultName = col.Modes[0].Name
		}
		colLookups[colID] = lu
	}

	// Map variables by collection
	varsByCol := make(map[string][]figma.Variable)
	for _, v := range meta.Variables {
		varsByCol[v.VariableCollectionID] = append(varsByCol[v.VariableCollectionID], v)
	}

	var collections []ResolvedCollection
	for _, colID := range colIDs {
		col := meta.VariableCollections[colID]
		lu := colLookups[colID]
		vars := varsByCol[colID]

		// Sort variables deterministically by name
		sort.Slice(vars, func(i, j int) bool {
			return vars[i].Name < vars[j].Name
		})

		var resolvedTokens []ResolvedToken
		for _, v := range vars {
			tokenValues := make(map[string]string, len(col.Modes))
			for _, m := range col.Modes {
				visited := make(map[string]bool)
				rawResolved := resolveAlias(meta, v, m.ModeID, visited, 0)
				formatted := formatVariableValue(v, rawResolved)
				tokenValues[m.Name] = formatted
			}

			resolvedTokens = append(resolvedTokens, ResolvedToken{
				ID:          v.ID,
				Name:        v.Name,
				CSSName:     Slugify(v.Name),
				Type:        v.ResolvedType,
				Description: v.Description,
				Values:      tokenValues,
			})
		}

		collections = append(collections, ResolvedCollection{
			ID:          col.ID,
			Name:        col.Name,
			Modes:       lu.modeNames,
			DefaultMode: lu.defaultName,
			Tokens:      resolvedTokens,
		})
	}

	return &DesignTokens{Collections: collections}, nil
}

// resolveAlias recursively resolves Figma VARIABLE_ALIAS references up to a max depth of 20.
func resolveAlias(meta *figma.VariablesMeta, v figma.Variable, modeID string, visited map[string]bool, depth int) any {
	if depth > 20 || visited[v.ID] {
		// Cycle detected or max recursion reached; fallback to raw value in mode
		return v.ValuesByMode[modeID]
	}
	visited[v.ID] = true

	val, exists := v.ValuesByMode[modeID]
	if !exists {
		// Try collection default mode
		if col, ok := meta.VariableCollections[v.VariableCollectionID]; ok {
			val = v.ValuesByMode[col.DefaultModeID]
		}
	}

	aliasMap, ok := val.(map[string]any)
	if !ok {
		return val
	}

	if aliasType, _ := aliasMap["type"].(string); aliasType == "VARIABLE_ALIAS" {
		targetID, _ := aliasMap["id"].(string)
		targetVar, found := meta.Variables[targetID]
		if !found {
			return val
		}

		// Find target mode matching current mode name or fallback
		targetModeID := findMatchingTargetMode(meta, v, targetVar, modeID)
		return resolveAlias(meta, targetVar, targetModeID, visited, depth+1)
	}

	return val
}

// findMatchingTargetMode finds a mode in targetVar's collection that matches source mode's name.
func findMatchingTargetMode(meta *figma.VariablesMeta, srcVar, targetVar figma.Variable, srcModeID string) string {
	srcCol, okSrc := meta.VariableCollections[srcVar.VariableCollectionID]
	targetCol, okTarget := meta.VariableCollections[targetVar.VariableCollectionID]
	if !okTarget {
		return ""
	}

	var srcModeName string
	if okSrc {
		for _, m := range srcCol.Modes {
			if m.ModeID == srcModeID {
				srcModeName = m.Name
				break
			}
		}
	}

	if srcModeName != "" {
		for _, tm := range targetCol.Modes {
			if strings.EqualFold(tm.Name, srcModeName) {
				return tm.ModeID
			}
		}
	}

	if targetCol.DefaultModeID != "" {
		return targetCol.DefaultModeID
	}
	if len(targetCol.Modes) > 0 {
		return targetCol.Modes[0].ModeID
	}
	return ""
}

// formatVariableValue formats a resolved raw value into a clean CSS/token string.
func formatVariableValue(v figma.Variable, raw any) string {
	if raw == nil {
		return ""
	}

	switch v.ResolvedType {
	case "COLOR":
		if m, ok := raw.(map[string]any); ok {
			if rgba, ok := transformers.ExtractRGBA(m); ok {
				return transformers.ConvertColor(rgba, 1.0)
			}
		}
		return fmt.Sprint(raw)

	case "FLOAT":
		var num float64
		switch n := raw.(type) {
		case float64:
			num = n
		case float32:
			num = float64(n)
		case int:
			num = float64(n)
		case json.Number:
			f, _ := n.Float64()
			num = f
		default:
			return fmt.Sprint(raw)
		}

		// Check if scope is opacity or unitless
		isUnitless := false
		lowerName := strings.ToLower(v.Name)
		if strings.Contains(lowerName, "opacity") || strings.Contains(lowerName, "alpha") {
			isUnitless = true
		}
		for _, s := range v.Scopes {
			if s == "OPACITY" {
				isUnitless = true
				break
			}
		}

		if isUnitless {
			return fmt.Sprintf("%g", math.Round(num*1000)/1000)
		}

		// If 0, output 0px
		if num == 0 {
			return "0px"
		}
		return fmt.Sprintf("%gpx", math.Round(num*100)/100)

	case "BOOLEAN":
		if b, ok := raw.(bool); ok {
			return fmt.Sprintf("%t", b)
		}
		return fmt.Sprint(raw)

	case "STRING":
		return fmt.Sprint(raw)

	default:
		return fmt.Sprint(raw)
	}
}

// ToCSS serializes DesignTokens into clean CSS variables with :root and theme classes.
func (dt *DesignTokens) ToCSS() string {
	if len(dt.Collections) == 0 {
		return "/* No variable collections found */\n"
	}

	// Discover all mode names across all collections
	// Default/Light modes go into :root
	// Dark mode goes into .dark
	// Other modes go into [data-theme="..."]
	type tokenEntry struct {
		cssName string
		val     string
		comment string
	}

	rootTokens := make([]tokenEntry, 0)
	otherModeTokens := make(map[string][]tokenEntry)

	for _, col := range dt.Collections {
		defaultMode := col.DefaultMode
		for _, token := range col.Tokens {
			if token.CSSName == "" {
				continue
			}

			// Add default mode to :root
			defVal := token.Values[defaultMode]
			if defVal != "" {
				rootTokens = append(rootTokens, tokenEntry{
					cssName: token.CSSName,
					val:     defVal,
					comment: col.Name,
				})
			}

			// Add non-default modes
			for modeName, val := range token.Values {
				if modeName == defaultMode || val == "" || val == defVal {
					continue
				}
				otherModeTokens[modeName] = append(otherModeTokens[modeName], tokenEntry{
					cssName: token.CSSName,
					val:     val,
					comment: col.Name,
				})
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("/**\n * Figma Design Tokens\n * Generated by fm tokens\n */\n\n")

	// 1. :root block
	sb.WriteString(":root {\n")
	lastCol := ""
	for _, te := range rootTokens {
		if te.comment != lastCol {
			if lastCol != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(fmt.Sprintf("  /* %s */\n", te.comment))
			lastCol = te.comment
		}
		sb.WriteString(fmt.Sprintf("  %s: %s;\n", te.cssName, te.val))
	}
	sb.WriteString("}\n")

	// 2. Additional mode blocks (sorted)
	otherModes := make([]string, 0, len(otherModeTokens))
	for m := range otherModeTokens {
		otherModes = append(otherModes, m)
	}
	sort.Strings(otherModes)

	for _, m := range otherModes {
		tokens := otherModeTokens[m]
		if len(tokens) == 0 {
			continue
		}

		sb.WriteString("\n")
		selector := formatModeSelector(m)
		sb.WriteString(fmt.Sprintf("%s {\n", selector))
		lastCol = ""
		for _, te := range tokens {
			if te.comment != lastCol {
				if lastCol != "" {
					sb.WriteString("\n")
				}
				sb.WriteString(fmt.Sprintf("  /* %s (%s) */\n", te.comment, m))
				lastCol = te.comment
			}
			sb.WriteString(fmt.Sprintf("  %s: %s;\n", te.cssName, te.val))
		}
		sb.WriteString("}\n")
	}

	return sb.String()
}

func formatModeSelector(modeName string) string {
	lower := strings.ToLower(strings.TrimSpace(modeName))
	if lower == "dark" {
		return ".dark"
	}
	clean := strings.Trim(nonAlphanumericSlugPattern.ReplaceAllString(lower, "-"), "-")
	return fmt.Sprintf(`[data-theme="%s"]`, clean)
}

// ToTailwind exports DesignTokens as a Tailwind CSS configuration extension (theme.extend).
func (dt *DesignTokens) ToTailwind() string {
	type categoryMap map[string]any

	colors := make(map[string]string)
	spacing := make(map[string]string)
	borderRadius := make(map[string]string)
	fontSize := make(map[string]string)
	other := make(map[string]string)

	for _, col := range dt.Collections {
		defaultMode := col.DefaultMode
		for _, token := range col.Tokens {
			val := token.Values[defaultMode]
			if val == "" {
				continue
			}

			key := strings.TrimPrefix(token.CSSName, "--")
			lowerName := strings.ToLower(token.Name)

			switch {
			case token.Type == "COLOR" || strings.HasPrefix(key, "color-") || strings.HasPrefix(key, "colors-"):
				k := strings.TrimPrefix(strings.TrimPrefix(key, "colors-"), "color-")
				colors[k] = val
			case strings.HasPrefix(key, "spacing-") || strings.HasPrefix(key, "space-") || strings.Contains(lowerName, "spacing"):
				k := strings.TrimPrefix(strings.TrimPrefix(key, "spacing-"), "space-")
				spacing[k] = val
			case strings.HasPrefix(key, "radius-") || strings.HasPrefix(key, "rounded-") || strings.Contains(lowerName, "radius"):
				k := strings.TrimPrefix(strings.TrimPrefix(key, "radius-"), "rounded-")
				borderRadius[k] = val
			case strings.HasPrefix(key, "font-size-") || strings.Contains(lowerName, "font size"):
				k := strings.TrimPrefix(key, "font-size-")
				fontSize[k] = val
			default:
				other[key] = val
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("/** @type {import('tailwindcss').Config} */\n")
	sb.WriteString("module.exports = {\n")
	sb.WriteString("  theme: {\n")
	sb.WriteString("    extend: {\n")

	writeSection := func(name string, m map[string]string) {
		if len(m) == 0 {
			return
		}
		sb.WriteString(fmt.Sprintf("      %s: {\n", name))
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("        '%s': '%s',\n", k, m[k]))
		}
		sb.WriteString("      },\n")
	}

	writeSection("colors", colors)
	writeSection("spacing", spacing)
	writeSection("borderRadius", borderRadius)
	writeSection("fontSize", fontSize)
	if len(other) > 0 {
		writeSection("tokens", other)
	}

	sb.WriteString("    },\n")
	sb.WriteString("  },\n")
	sb.WriteString("};\n")

	return sb.String()
}

// ToJSON formats DesignTokens as indented JSON.
func (dt *DesignTokens) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(dt, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal design tokens to JSON: %w", err)
	}
	return string(bytes), nil
}

// Export processes Figma VariablesMeta and exports it according to the requested FormatType.
func Export(meta *figma.VariablesMeta, format FormatType) (string, error) {
	tokens, err := ProcessVariables(meta)
	if err != nil {
		return "", err
	}

	switch format {
	case FormatCSS, "":
		return tokens.ToCSS(), nil
	case FormatTailwind:
		return tokens.ToTailwind(), nil
	case FormatJSON:
		return tokens.ToJSON()
	default:
		return "", fmt.Errorf("unsupported format %q", format)
	}
}
