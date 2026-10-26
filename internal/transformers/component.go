package transformers

import "strings"

// StripPropertyNameSuffix strips Figma's internal "#nodeId" suffix from component property names.
// Example: "On Sale#341:0" -> "On Sale".
func StripPropertyNameSuffix(name string) string {
	if idx := strings.IndexByte(name, '#'); idx >= 0 {
		return name[:idx]
	}
	return name
}

// SimplifyComponentProperties extracts BOOLEAN and TEXT instance properties into a flat map.
func SimplifyComponentProperties(n map[string]any) map[string]any {
	rawProps, ok := n["componentProperties"].(map[string]any)
	if !ok || len(rawProps) == 0 {
		return nil
	}
	out := make(map[string]any)
	for rawName, val := range rawProps {
		propMap, ok := val.(map[string]any)
		if !ok {
			continue
		}
		propType := getString(propMap, "type")
		if propType != "BOOLEAN" && propType != "TEXT" {
			continue
		}
		cleanName := StripPropertyNameSuffix(rawName)
		if v, exists := propMap["value"]; exists {
			out[cleanName] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SimplifyPropertyReferences extracts "visible" and "characters" (renamed to "text") references.
func SimplifyPropertyReferences(n map[string]any) map[string]string {
	rawRefs, ok := n["componentPropertyReferences"].(map[string]any)
	if !ok || len(rawRefs) == 0 {
		return nil
	}
	out := make(map[string]string)
	if vis, ok := rawRefs["visible"].(string); ok && vis != "" {
		out["visible"] = StripPropertyNameSuffix(vis)
	}
	if chars, ok := rawRefs["characters"].(string); ok && chars != "" {
		out["text"] = StripPropertyNameSuffix(chars)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SimplifyPropertyDefinitions extracts BOOLEAN and TEXT property definitions on COMPONENT or COMPONENT_SET nodes.
func SimplifyPropertyDefinitions(n map[string]any) map[string]any {
	rawDefs, ok := n["componentPropertyDefinitions"].(map[string]any)
	if !ok || len(rawDefs) == 0 {
		return nil
	}
	out := make(map[string]any)
	for rawName, val := range rawDefs {
		defMap, ok := val.(map[string]any)
		if !ok {
			continue
		}
		propType := getString(defMap, "type")
		if propType != "BOOLEAN" && propType != "TEXT" {
			continue
		}
		cleanName := StripPropertyNameSuffix(rawName)
		out[cleanName] = map[string]any{
			"type":         strings.ToLower(propType),
			"defaultValue": defMap["defaultValue"],
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
