package serialize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ducconit/figma-cli/internal/extractors"
	"gopkg.in/yaml.v3"
)

// SerializableDesign is the wrapped structure prepared for output serialization.
type SerializableDesign struct {
	Metadata   map[string]any                                       `json:"metadata" yaml:"metadata"`
	Components map[string]extractors.SimplifiedComponentDefinition  `json:"components,omitempty" yaml:"components,omitempty"`
	CompSets   map[string]extractors.SimplifiedComponentSetDefinition `json:"componentSets,omitempty" yaml:"componentSets,omitempty"`
	GlobalVars extractors.GlobalVars                                `json:"globalVars" yaml:"globalVars"`
	Elements   map[string]extractors.ElementBody                    `json:"elements,omitempty" yaml:"elements,omitempty"`
	Nodes      []*extractors.SimplifiedNode                         `json:"nodes" yaml:"nodes"`
}

// WrapForSerialization strips noise layer names and builds a SerializableDesign.
func WrapForSerialization(design *extractors.SimplifiedDesign) SerializableDesign {
	cleanedNodes := StripNoiseNames(design.Nodes, design.Elements)
	return SerializableDesign{
		Metadata: map[string]any{
			"name": design.Name,
		},
		Nodes:      cleanedNodes,
		Components: design.Components,
		CompSets:   design.ComponentSets,
		GlobalVars: design.GlobalVars,
		Elements:   design.Elements,
	}
}

// Serialize formats a SimplifiedDesign into "tree" (default compact DSL), "yaml", or "json".
func Serialize(design *extractors.SimplifiedDesign, format string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "tree"
	}
	wrapped := WrapForSerialization(design)

	switch format {
	case "tree":
		return serializeAsTree(wrapped)
	case "yaml", "yml":
		return dumpYAML(wrapped)
	case "json":
		b, err := json.MarshalIndent(wrapped, "", "  ")
		if err != nil {
			return "", fmt.Errorf("marshal json: %w", err)
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("invalid output format %q: expected one of tree, yaml, json", format)
	}
}

func dumpYAML(v any) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("encode yaml: %w", err)
	}
	_ = enc.Close()
	return strings.TrimRight(buf.String(), "\n"), nil
}

func serializeAsTree(d SerializableDesign) (string, error) {
	var sections []string

	name, _ := d.Metadata["name"].(string)
	nameJSON, _ := json.Marshal(name)
	sections = append(sections, fmt.Sprintf("NAME: %s", string(nameJSON)))

	if len(d.GlobalVars.Styles) > 0 {
		y, err := dumpYAML(d.GlobalVars.Styles)
		if err != nil {
			return "", err
		}
		sections = append(sections, "GLOBAL_VARS:\n"+y)
	}

	if len(d.Elements) > 0 {
		y, err := dumpYAML(d.Elements)
		if err != nil {
			return "", err
		}
		sections = append(sections, "ELEMENTS:\n"+y)
	}

	if len(d.Components) > 0 {
		y, err := dumpYAML(d.Components)
		if err != nil {
			return "", err
		}
		sections = append(sections, "COMPONENTS:\n"+y)
	}

	if len(d.CompSets) > 0 {
		y, err := dumpYAML(d.CompSets)
		if err != nil {
			return "", err
		}
		sections = append(sections, "COMPONENT_SETS:\n"+y)
	}

	var nodeLines []string
	var walk func(list []*extractors.SimplifiedNode, depth int)
	walk = func(list []*extractors.SimplifiedNode, depth int) {
		for _, n := range list {
			nodeLines = append(nodeLines, renderTreeNodeLine(n, depth, d.Elements))
			if len(n.Children) > 0 {
				walk(n.Children, depth+1)
			}
		}
	}
	walk(d.Nodes, 0)
	sections = append(sections, "NODES:\n"+strings.Join(nodeLines, "\n"))

	return strings.Join(sections, "\n\n"), nil
}

func renderTreeNodeLine(n *extractors.SimplifiedNode, depth int, elements map[string]extractors.ElementBody) string {
	indent := strings.Repeat("  ", depth)
	effectiveType := n.Type
	if effectiveType == "" && n.Template != "" && elements != nil {
		if body, ok := elements[n.Template]; ok {
			effectiveType, _ = body["type"].(string)
		}
	}
	if effectiveType == "" {
		effectiveType = "NODE"
	}

	var tokens []string
	tokens = append(tokens, fmt.Sprintf("[%s]", effectiveType))
	if n.Name != "" {
		quoted, _ := json.Marshal(n.Name)
		tokens = append(tokens, string(quoted))
	}
	if n.ID != "" {
		tokens = append(tokens, "#"+n.ID)
	}

	appendKV := func(k string, v any) {
		if v == nil {
			return
		}
		switch val := v.(type) {
		case string:
			if val == "" {
				return
			}
			tokens = append(tokens, fmt.Sprintf("%s=%s", k, maybeQuoteScalar(val, k == "text")))
		case *float64:
			if val != nil {
				tokens = append(tokens, fmt.Sprintf("%s=%g", k, *val))
			}
		case *int:
			if val != nil {
				tokens = append(tokens, fmt.Sprintf("%s=%d", k, *val))
			}
		default:
			b, err := json.Marshal(val)
			if err == nil && string(b) != "null" && string(b) != "{}" && string(b) != "[]" {
				tokens = append(tokens, fmt.Sprintf("%s=%s", k, string(b)))
			}
		}
	}

	appendKV("template", n.Template)
	appendKV("layout", n.Layout)
	appendKV("fills", n.Fills)
	appendKV("strokes", n.Strokes)
	appendKV("strokeWeight", n.StrokeWeight)
	appendKV("strokeWeights", n.StrokeWeights)
	if len(n.StrokeDashes) > 0 {
		appendKV("strokeDashes", n.StrokeDashes)
	}
	appendKV("strokeAlign", n.StrokeAlign)
	appendKV("effects", n.Effects)
	appendKV("opacity", n.Opacity)
	appendKV("borderRadius", n.BorderRadius)
	if len(n.Styles) > 0 {
		appendKV("styles", n.Styles)
	}
	appendKV("componentId", n.ComponentID)
	if len(n.ComponentProperties) > 0 {
		appendKV("componentProperties", n.ComponentProperties)
	}
	if len(n.ComponentPropertyReferences) > 0 {
		appendKV("componentPropertyReferences", n.ComponentPropertyReferences)
	}
	appendKV("textStyle", n.TextStyle)
	appendKV("boldWeight", n.BoldWeight)
	appendKV("text", n.Text)

	return indent + strings.Join(tokens, " ")
}

func maybeQuoteScalar(s string, alwaysQuote bool) string {
	if alwaysQuote || strings.ContainsAny(s, " \t\n\"={}[],") {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return s
}
