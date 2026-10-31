package stitch

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ducconit/figma-cli/internal/extractors"
	"github.com/ducconit/figma-cli/internal/figma"
)

func TestSerializeStitchBasicAndMarkdown(t *testing.T) {
	spec := &DesignSystemSpec{
		Version:     "alpha",
		Name:        "StitchMock",
		Description: "A mock design system for unit testing.",
		Colors: map[string]string{
			"primary":   "#3B82F6",
			"secondary": "#1E293B",
			"white":     "#FFFFFF",
		},
		Typography: map[string]TypographyToken{
			"heading-1": {FontFamily: "Inter", FontSize: "28px", FontWeight: 700, LineHeight: "42px"},
			"body-md":   {FontFamily: "Inter", FontSize: "14px", FontWeight: 400, LineHeight: "21px"},
		},
		Spacing: map[string]string{
			"xs": "4px",
			"sm": "8px",
			"md": "16px",
		},
		Rounded: map[string]string{
			"none": "0px",
			"sm":   "4px",
			"full": "9999px",
		},
		Components: map[string]map[string]string{
			"button-primary": {
				"backgroundColor": "{colors.primary}",
				"textColor":       "{colors.white}",
				"rounded":         "{rounded.sm}",
				"padding":         "8px 16px",
				"typography":      "{typography.body-md}",
			},
		},
	}

	content, err := SerializeStitch(spec)
	if err != nil {
		t.Fatalf("SerializeStitch error: %v", err)
	}

	// Verify Frontmatter delimiters
	if !strings.HasPrefix(content, "---\n") {
		t.Fatalf("expected content to start with '---\\n', got:\n%s", content[:50])
	}
	if !strings.Contains(content, "\n---\n\n# StitchMock Design System Specification") {
		t.Fatalf("expected closing frontmatter and header title in output")
	}

	// Verify required sections
	requiredSnippets := []string{
		"version: alpha",
		"name: StitchMock",
		"colors:",
		"primary: \"#3B82F6\"",
		"typography:",
		"heading-1:",
		"fontFamily: Inter",
		"fontWeight: 700",
		"spacing:",
		"sm: 8px",
		"rounded:",
		"sm: 4px",
		"components:",
		"button-primary:",
		"backgroundColor: \"{colors.primary}\"",
		"## Color Tokens",
		"| `primary` | `#3B82F6` |",
		"## Typography Scale",
		"| `heading-1` | `Inter` | `28px` | `700` | `42px` |",
		"## Spacing Tokens",
		"## Border Radius Tokens",
		"## Component Token Specs",
	}

	for _, s := range requiredSnippets {
		if !strings.Contains(content, s) {
			t.Errorf("expected serialized DESIGN.md to contain snippet: %q", s)
		}
	}

	// Verify JSON serialization
	jsonOut, err := spec.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON error: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(jsonOut), "{") || !strings.Contains(jsonOut, `"version": "alpha"`) {
		t.Fatalf("invalid JSON output from ToJSON:\n%s", jsonOut)
	}
}

func TestValidateAndFixReferences(t *testing.T) {
	spec := &DesignSystemSpec{
		Colors: map[string]string{
			"primary": "#3B82F6",
		},
		Rounded: map[string]string{
			"sm": "4px",
		},
		Typography: map[string]TypographyToken{
			"body-lg": {FontFamily: "Inter", FontSize: "16px", FontWeight: 500, LineHeight: "24px"},
		},
		Components: map[string]map[string]string{
			"button-primary": {
				"backgroundColor": "{colors.unknown-brand}",
				"rounded":         "{rounded.default}",
				"typography":      "{typography.unknown-font}",
			},
		},
	}

	validateAndFixReferences(spec)

	btn := spec.Components["button-primary"]
	if btn["backgroundColor"] != "{colors.primary}" {
		t.Errorf("expected fallback to {colors.primary}, got %q", btn["backgroundColor"])
	}
	if btn["rounded"] != "{rounded.sm}" {
		t.Errorf("expected fallback to {rounded.sm}, got %q", btn["rounded"])
	}
	if btn["typography"] != "{typography.body-lg}" {
		t.Errorf("expected fallback to {typography.body-lg}, got %q", btn["typography"])
	}
}

func TestExtractFromSimplifiedDesign(t *testing.T) {
	design := &extractors.SimplifiedDesign{
		Name: "Mock Dashboard (Community)",
		GlobalVars: extractors.GlobalVars{
			Styles: map[string]any{
				"Headings/Heading 1": map[string]any{
					"fontFamily": "Nunito Sans",
					"fontSize":   float64(28),
					"fontWeight": float64(700),
					"lineHeight": "42px",
				},
				"Brand / Accent": map[string]any{
					"fills": []any{"#FE9F43"},
				},
			},
		},
		Nodes: []*extractors.SimplifiedNode{
			{
				Name: "Color Palette Frame",
				Children: []*extractors.SimplifiedNode{
					{
						Name:  "Primary / 500",
						Fills: "#FE9F43",
					},
					{
						Name: "System Color / Success",
						Children: []*extractors.SimplifiedNode{
							{
								Name:  "Swatch",
								Fills: "#28C76F",
							},
							{
								Name: "Label",
								Type: "TEXT",
								Text: "Success Light",
							},
						},
					},
				},
			},
			{
				Name:         "Button",
				Type:         "COMPONENT",
				BorderRadius: "5px",
			},
		},
	}

	spec := ExtractFromSimplifiedDesign(design, nil, ExtractionOptions{})

	if spec.Name != "Mock Dashboard" {
		t.Errorf("expected cleaned name 'Mock Dashboard', got %q", spec.Name)
	}

	if spec.Colors["accent"] != "#FE9F43" && spec.Colors["primary-500"] != "#FE9F43" {
		t.Errorf("expected primary/accent color #FE9F43, got: %+v", spec.Colors)
	}

	if spec.Colors["success-light"] != "#28C76F" && spec.Colors["success"] != "#28C76F" {
		t.Errorf("expected success color #28C76F, got: %+v", spec.Colors)
	}

	if tok, ok := spec.Typography["heading-1"]; !ok || tok.FontFamily != "Nunito Sans" || tok.FontSize != "28px" {
		t.Errorf("expected heading-1 typography, got: %+v", tok)
	}

	if spec.Rounded["sm"] != "5px" && spec.Rounded["sm"] != "4px" {
		t.Errorf("expected rounded.sm, got: %+v", spec.Rounded)
	}
}

func TestVariablesExtraction(t *testing.T) {
	meta := &figma.VariablesMeta{
		Variables: map[string]figma.Variable{
			"var_col_1": {
				ID:                   "var_col_1",
				Name:                 "Colors/Primary/Main",
				ResolvedType:         "COLOR",
				VariableCollectionID: "col_1",
				ValuesByMode: map[string]any{
					"mode_1": map[string]any{
						"r": 0.23,
						"g": 0.51,
						"b": 0.96,
						"a": 1.0,
					},
				},
			},
			"var_rad_1": {
				ID:                   "var_rad_1",
				Name:                 "Radius/Corner-SM",
				ResolvedType:         "FLOAT",
				VariableCollectionID: "col_1",
				Scopes:               []string{"CORNER_RADIUS"},
				ValuesByMode: map[string]any{
					"mode_1": float64(6),
				},
			},
		},
		VariableCollections: map[string]figma.VariableCollection{
			"col_1": {
				ID:            "col_1",
				Name:          "Tokens",
				DefaultModeID: "mode_1",
				Modes: []figma.VariableMode{
					{ModeID: "mode_1", Name: "Value"},
				},
			},
		},
	}

	spec := ExtractFromSimplifiedDesign(nil, meta, ExtractionOptions{})
	if _, ok := spec.Colors["primary-main"]; !ok {
		t.Errorf("expected variable color 'primary-main', got: %+v", spec.Colors)
	}
	if val, ok := spec.Rounded["corner-sm"]; !ok || val != "6px" {
		t.Errorf("expected rounded 'corner-sm' with '6px', got: %q", val)
	}
}

func TestEndToEndStitchLintCompatibility(t *testing.T) {
	// If npx is available, verify that generated output passes official @google/design.md linter with 0 errors
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available, skipping live linter check")
	}

	spec := &DesignSystemSpec{
		Version:     "alpha",
		Name:        "DreamsPOS",
		Description: "Design system specification for DreamsPOS POS & Inventory Management.",
		Colors: map[string]string{
			"primary":      "#FE9F43",
			"secondary":    "#092C4C",
			"white":        "#FFFFFF",
			"surface":      "#FFFFFF",
			"neutral":      "#212B36",
			"border":       "#BCBFC3",
			"success":      "#28C76F",
			"warning":      "#FFCA18",
			"danger":       "#FF0000",
			"error":        "#FF0000",
			"info":         "#155EEF",
			"tint-success": "#ECFAF2",
		},
		Typography: map[string]TypographyToken{
			"heading-1": {FontFamily: "Nunito Sans", FontSize: "28px", FontWeight: 700, LineHeight: "42px"},
			"body-lg":   {FontFamily: "Nunito Sans", FontSize: "14px", FontWeight: 500, LineHeight: "21px"},
			"body-xs":   {FontFamily: "Nunito Sans", FontSize: "10px", FontWeight: 600, LineHeight: "15px"},
		},
		Spacing: map[string]string{
			"xs": "4px",
			"sm": "8px",
			"md": "12px",
			"lg": "16px",
		},
		Rounded: map[string]string{
			"none": "0px",
			"sm":   "5px",
			"full": "9999px",
		},
		Components: map[string]map[string]string{
			"button-primary": {
				"backgroundColor": "{colors.primary}",
				"textColor":       "{colors.white}",
				"rounded":         "{rounded.sm}",
				"padding":         "8px 16px",
				"typography":      "{typography.body-lg}",
			},
			"badge-success": {
				"backgroundColor": "{colors.tint-success}",
				"textColor":       "{colors.success}",
				"rounded":         "{rounded.sm}",
				"padding":         "4px 8px",
				"typography":      "{typography.body-xs}",
			},
		},
	}

	rendered, err := SerializeStitch(spec)
	if err != nil {
		t.Fatalf("SerializeStitch error: %v", err)
	}

	tmpFile, err := os.CreateTemp("", "design-*.md")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(rendered); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_ = tmpFile.Close()

	cmd := exec.Command("npx", "-y", "@google/design.md", "lint", tmpFile.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("official @google/design.md lint failed:\n%s", string(out))
	}
}
