package motion

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// FormatType defines output formats for motion specifications.
type FormatType string

const (
	FormatCSS    FormatType = "css"
	FormatFramer FormatType = "framer"
	FormatJSON   FormatType = "json"
)

// ParseFormat validates and normalizes a motion output format string.
func ParseFormat(raw string) (FormatType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "css", "":
		return FormatCSS, nil
	case "framer", "motion", "framer-motion":
		return FormatFramer, nil
	case "json":
		return FormatJSON, nil
	default:
		return "", fmt.Errorf("unsupported motion format %q (allowed: css, framer, json)", raw)
	}
}

// TransitionSpec holds a single extracted prototype interaction or node transition.
type TransitionSpec struct {
	NodeID         string     `json:"nodeId"`
	NodeName       string     `json:"nodeName"`
	Trigger        string     `json:"trigger"`
	ActionType     string     `json:"actionType,omitempty"`
	DestinationID  string     `json:"destinationId,omitempty"`
	TransitionType string     `json:"transitionType"`
	Direction      string     `json:"direction,omitempty"`
	DurationMs     float64    `json:"durationMs"`
	EasingType     string     `json:"easingType"`
	CubicBezier    [4]float64 `json:"cubicBezier"`
	CSSKeyframes   string     `json:"cssKeyframes,omitempty"`
	CSSTransition  string     `json:"cssTransition"`
}

// MotionReport contains all transitions extracted from a Figma file or node subtree.
type MotionReport struct {
	FileName    string           `json:"fileName"`
	Transitions []TransitionSpec `json:"transitions"`
}

var nonAlphaNumPattern = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func slugifyIdentifier(name, fallback string) string {
	clean := strings.Trim(nonAlphaNumPattern.ReplaceAllString(strings.TrimSpace(name), "-"), "-")
	if clean == "" {
		return fallback
	}
	return strings.ToLower(clean)
}

func cleanComment(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "*/", "* /")
	return strings.TrimSpace(s)
}

// mapEasingToBezier maps Figma easing types to CSS cubic-bezier control points [x1, y1, x2, y2].
func mapEasingToBezier(easingType string, custom *[4]float64) [4]float64 {
	if custom != nil {
		return *custom
	}
	switch strings.ToUpper(strings.TrimSpace(easingType)) {
	case "LINEAR":
		return [4]float64{0, 0, 1, 1}
	case "EASE_IN":
		return [4]float64{0.42, 0, 1, 1}
	case "EASE_OUT":
		return [4]float64{0, 0, 0.58, 1}
	case "EASE_IN_AND_OUT":
		return [4]float64{0.42, 0, 0.58, 1}
	case "EASE_IN_BACK":
		return [4]float64{0.3, -0.05, 0.7, -0.5}
	case "EASE_OUT_BACK":
		return [4]float64{0.45, 1.45, 0.8, 1}
	case "EASE_IN_AND_OUT_BACK":
		return [4]float64{0.7, -0.4, 0.4, 1.4}
	case "GENTLE":
		return [4]float64{0.37, 0, 0.63, 1}
	case "QUICK":
		return [4]float64{0.2, 0.9, 0.3, 1}
	case "BOUNCY":
		return [4]float64{0.34, 1.56, 0.64, 1}
	case "SLOW":
		return [4]float64{0.5, 0, 0.2, 1}
	default:
		// Standard ease-out default
		return [4]float64{0.25, 0.1, 0.25, 1}
	}
}

func formatBezierCSS(bz [4]float64) string {
	return fmt.Sprintf("cubic-bezier(%g, %g, %g, %g)",
		roundFloat(bz[0]), roundFloat(bz[1]), roundFloat(bz[2]), roundFloat(bz[3]))
}

func roundFloat(v float64) float64 {
	return math.Round(v*1000) / 1000
}

func extractFloat(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	val, exists := m[key]
	if !exists {
		return 0, false
	}
	switch n := val.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func extractCustomBezier(m map[string]any) *[4]float64 {
	if m == nil {
		return nil
	}
	cbMap, ok := m["easingFunctionCubicBezier"].(map[string]any)
	if !ok {
		return nil
	}
	x1, ok1 := extractFloat(cbMap, "x1")
	y1, ok2 := extractFloat(cbMap, "y1")
	x2, ok3 := extractFloat(cbMap, "x2")
	y2, ok4 := extractFloat(cbMap, "y2")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return nil
	}
	res := [4]float64{x1, y1, x2, y2}
	return &res
}

func buildKeyframesName(nodeName, transitionType, direction string) string {
	base := slugifyIdentifier(nodeName, "element")
	tType := slugifyIdentifier(transitionType, "transition")
	if direction != "" {
		return fmt.Sprintf("fm-%s-%s-%s", base, tType, strings.ToLower(direction))
	}
	return fmt.Sprintf("fm-%s-%s", base, tType)
}

// ExtractMotion walks a raw Figma GetFile or GetFileNodes JSON map and collects all prototype transitions and interactions.
func ExtractMotion(raw map[string]any) (*MotionReport, error) {
	if raw == nil {
		return nil, fmt.Errorf("empty Figma response")
	}

	fileName, _ := raw["name"].(string)
	var rootNodes []map[string]any

	if doc, ok := raw["document"].(map[string]any); ok {
		rootNodes = append(rootNodes, doc)
	} else if nodesMap, ok := raw["nodes"].(map[string]any); ok {
		keys := make([]string, 0, len(nodesMap))
		for k := range nodesMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if wrapper, ok := nodesMap[k].(map[string]any); ok {
				if doc, ok := wrapper["document"].(map[string]any); ok {
					rootNodes = append(rootNodes, doc)
				}
			}
		}
	}

	var specs []TransitionSpec
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		if node == nil {
			return
		}
		if vis, ok := node["visible"].(bool); ok && !vis {
			return
		}

		nodeID, _ := node["id"].(string)
		nodeName, _ := node["name"].(string)

		// 1. Check modern `interactions` array first
		hasModernInteractions := false
		if rawInteractions, ok := node["interactions"].([]any); ok && len(rawInteractions) > 0 {
			for _, rawInt := range rawInteractions {
				intMap, ok := rawInt.(map[string]any)
				if !ok {
					continue
				}
				triggerType := "ON_CLICK"
				if trig, ok := intMap["trigger"].(map[string]any); ok {
					if tt, _ := trig["type"].(string); tt != "" {
						triggerType = tt
					}
				}
				actions, _ := intMap["actions"].([]any)
				for _, rawAct := range actions {
					actMap, ok := rawAct.(map[string]any)
					if !ok {
						continue
					}
					actType, _ := actMap["type"].(string)
					destID, _ := actMap["destinationId"].(string)
					transMap, hasTrans := actMap["transition"].(map[string]any)
					if !hasTrans && destID == "" {
						continue
					}

					hasModernInteractions = true
					transType := "SMART_ANIMATE"
					direction := ""
					durationSec := 0.3
					easingType := "EASE_OUT"
					var customBez *[4]float64

					if hasTrans {
						if tt, _ := transMap["type"].(string); tt != "" {
							transType = tt
						}
						direction, _ = transMap["direction"].(string)
						if d, ok := extractFloat(transMap, "duration"); ok && d > 0 {
							durationSec = d
						}
						if easMap, ok := transMap["easing"].(map[string]any); ok {
							if et, _ := easMap["type"].(string); et != "" {
								easingType = et
							}
							customBez = extractCustomBezier(easMap)
						}
					}

					durMs := roundFloat(durationSec * 1000)
					bez := mapEasingToBezier(easingType, customBez)
					kfName := buildKeyframesName(nodeName, transType, direction)
					cssTrans := fmt.Sprintf("all %gms %s", durMs, formatBezierCSS(bez))

					specs = append(specs, TransitionSpec{
						NodeID:         nodeID,
						NodeName:       nodeName,
						Trigger:        triggerType,
						ActionType:     actType,
						DestinationID:  destID,
						TransitionType: transType,
						Direction:      direction,
						DurationMs:     durMs,
						EasingType:     easingType,
						CubicBezier:    bez,
						CSSKeyframes:   kfName,
						CSSTransition:  cssTrans,
					})
				}
			}
		}

		// 2. Check legacy node-level transition properties if not already captured by interactions
		if !hasModernInteractions {
			destID, _ := node["transitionNodeID"].(string)
			rawEasing, _ := node["transitionEasing"].(string)
			durSec, hasDur := extractFloat(node, "transitionDuration")

			if destID != "" || rawEasing != "" || hasDur {
				if !hasDur || durSec <= 0 {
					durSec = 0.3
				}
				if rawEasing == "" {
					rawEasing = "EASE_OUT"
				}
				durMs := roundFloat(durSec * 1000)
				bez := mapEasingToBezier(rawEasing, extractCustomBezier(node))
				kfName := buildKeyframesName(nodeName, "smart-animate", "")
				cssTrans := fmt.Sprintf("all %gms %s", durMs, formatBezierCSS(bez))

				specs = append(specs, TransitionSpec{
					NodeID:         nodeID,
					NodeName:       nodeName,
					Trigger:        "ON_CLICK",
					ActionType:     "NODE",
					DestinationID:  destID,
					TransitionType: "SMART_ANIMATE",
					DurationMs:     durMs,
					EasingType:     rawEasing,
					CubicBezier:    bez,
					CSSKeyframes:   kfName,
					CSSTransition:  cssTrans,
				})
			}
		}

		if children, ok := node["children"].([]any); ok {
			for _, ch := range children {
				if chMap, ok := ch.(map[string]any); ok {
					walk(chMap)
				}
			}
		}
	}

	for _, rn := range rootNodes {
		walk(rn)
	}

	return &MotionReport{
		FileName:    fileName,
		Transitions: specs,
	}, nil
}

// ToCSS formats a MotionReport into CSS transition variables, utility classes, and @keyframes blocks.
func (r *MotionReport) ToCSS() string {
	if len(r.Transitions) == 0 {
		return "/* No prototype transitions or motion properties found */\n"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("/**\n * Figma Motion & Transitions (%s)\n * Generated by fm motion --format css\n */\n\n", cleanComment(r.FileName)))

	for _, t := range r.Transitions {
		slug := slugifyIdentifier(t.NodeName, "node")
		bezStr := formatBezierCSS(t.CubicBezier)
		sb.WriteString(fmt.Sprintf("/* Node %s (%s) — %s -> %s (%s) */\n",
			cleanComment(t.NodeID), cleanComment(t.NodeName), cleanComment(t.Trigger), cleanComment(t.TransitionType), cleanComment(t.EasingType)))
		sb.WriteString(fmt.Sprintf(".motion-%s {\n", slug))
		sb.WriteString(fmt.Sprintf("  transition: %s;\n", t.CSSTransition))
		sb.WriteString(fmt.Sprintf("  animation: %s %gms %s both;\n", t.CSSKeyframes, t.DurationMs, bezStr))
		sb.WriteString("}\n\n")

		sb.WriteString(fmt.Sprintf("@keyframes %s {\n", t.CSSKeyframes))
		switch strings.ToUpper(t.TransitionType) {
		case "MOVE_IN", "SLIDE_IN", "PUSH":
			offset := "translateY(16px)"
			switch strings.ToUpper(t.Direction) {
			case "LEFT":
				offset = "translateX(-16px)"
			case "RIGHT":
				offset = "translateX(16px)"
			case "TOP":
				offset = "translateY(-16px)"
			}
			sb.WriteString(fmt.Sprintf("  from {\n    opacity: 0;\n    transform: %s;\n  }\n", offset))
			sb.WriteString("  to {\n    opacity: 1;\n    transform: translate(0, 0);\n  }\n")
		default:
			sb.WriteString("  from {\n    opacity: 0;\n    transform: scale(0.98);\n  }\n")
			sb.WriteString("  to {\n    opacity: 1;\n    transform: scale(1);\n  }\n")
		}
		sb.WriteString("}\n\n")
	}

	return strings.TrimRight(sb.String(), "\n") + "\n"
}

// ToFramer formats a MotionReport into Framer Motion / motion.dev transition definitions.
func (r *MotionReport) ToFramer() string {
	if len(r.Transitions) == 0 {
		return "// No prototype transitions or motion properties found\nexport const motionTransitions = {};\n"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("/**\n * Figma Motion & Transitions (%s)\n * Generated by fm motion --format framer\n */\n\n", cleanComment(r.FileName)))

	exportConst := "export const motionTransitions = {\n"
	sb.WriteString(exportConst)

	seen := make(map[string]int)
	for _, t := range r.Transitions {
		baseKey := slugifyIdentifier(t.NodeName, "node")
		seen[baseKey]++
		key := baseKey
		if c := seen[baseKey]; c > 1 {
			key = fmt.Sprintf("%s-%d", baseKey, c)
		}

		durSec := roundFloat(t.DurationMs / 1000.0)
		bz := t.CubicBezier
		sb.WriteString(fmt.Sprintf("  // %s (nodeId: %s, trigger: %s, type: %s)\n",
			cleanComment(t.NodeName), cleanComment(t.NodeID), cleanComment(t.Trigger), cleanComment(t.TransitionType)))
		sb.WriteString(fmt.Sprintf("  %q: {\n", key))
		sb.WriteString("    initial: { opacity: 0, y: 8 },\n")
		sb.WriteString("    animate: { opacity: 1, y: 0 },\n")
		sb.WriteString(fmt.Sprintf("    transition: { duration: %g, ease: [%g, %g, %g, %g] },\n",
			durSec, roundFloat(bz[0]), roundFloat(bz[1]), roundFloat(bz[2]), roundFloat(bz[3])))
		sb.WriteString("  },\n")
	}

	sb.WriteString("} as const;\n")
	return sb.String()
}

// ToJSON serializes the MotionReport as indented JSON.
func (r *MotionReport) ToJSON() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal motion report to JSON: %w", err)
	}
	return string(b), nil
}

// Export formats a MotionReport in the requested FormatType.
func Export(report *MotionReport, format FormatType) (string, error) {
	if report == nil {
		return "", fmt.Errorf("nil motion report")
	}
	switch format {
	case FormatCSS, "":
		return report.ToCSS(), nil
	case FormatFramer:
		return report.ToFramer(), nil
	case FormatJSON:
		return report.ToJSON()
	default:
		return "", fmt.Errorf("unsupported motion format %q", format)
	}
}
