package tokens

import (
	"strings"
	"testing"

	"github.com/ducconit/figma-cli/internal/figma"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "--token"},
		{"   ", "--token"},
		{"primary", "--primary"},
		{"Colors/Primary/500", "--colors-primary-500"},
		{"Space / 4", "--space-4"},
		{"Font Size.Heading 1", "--font-size-heading-1"},
		{"border_radius_lg", "--border-radius-lg"},
		{"Special @#$ Characters", "--special-characters"},
	}

	for _, tt := range tests {
		got := Slugify(tt.input)
		if got != tt.expected {
			t.Errorf("Slugify(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestProcessVariablesAndCycleDetection(t *testing.T) {
	meta := &figma.VariablesMeta{
		VariableCollections: map[string]figma.VariableCollection{
			"col-1": {
				ID:            "col-1",
				Name:          "Theme",
				DefaultModeID: "m-light",
				Modes: []figma.VariableMode{
					{ModeID: "m-light", Name: "Light"},
					{ModeID: "m-dark", Name: "Dark"},
				},
			},
		},
		Variables: map[string]figma.Variable{
			"v-color-raw": {
				ID:                   "v-color-raw",
				Name:                 "Colors/Primary",
				VariableCollectionID: "col-1",
				ResolvedType:         "COLOR",
				ValuesByMode: map[string]any{
					"m-light": map[string]any{"r": 0.2, "g": 0.4, "b": 0.8, "a": 1.0},
					"m-dark":  map[string]any{"r": 0.4, "g": 0.6, "b": 1.0, "a": 1.0},
				},
			},
			"v-color-alias": {
				ID:                   "v-color-alias",
				Name:                 "Colors/Action",
				VariableCollectionID: "col-1",
				ResolvedType:         "COLOR",
				ValuesByMode: map[string]any{
					"m-light": map[string]any{"type": "VARIABLE_ALIAS", "id": "v-color-raw"},
					"m-dark":  map[string]any{"type": "VARIABLE_ALIAS", "id": "v-color-raw"},
				},
			},
			// Cycle detection: v-loop1 -> v-loop2 -> v-loop1
			"v-loop1": {
				ID:                   "v-loop1",
				Name:                 "Loop1",
				VariableCollectionID: "col-1",
				ResolvedType:         "STRING",
				ValuesByMode: map[string]any{
					"m-light": map[string]any{"type": "VARIABLE_ALIAS", "id": "v-loop2"},
				},
			},
			"v-loop2": {
				ID:                   "v-loop2",
				Name:                 "Loop2",
				VariableCollectionID: "col-1",
				ResolvedType:         "STRING",
				ValuesByMode: map[string]any{
					"m-light": map[string]any{"type": "VARIABLE_ALIAS", "id": "v-loop1"},
				},
			},
			"v-float-space": {
				ID:                   "v-float-space",
				Name:                 "Spacing/4",
				VariableCollectionID: "col-1",
				ResolvedType:         "FLOAT",
				ValuesByMode: map[string]any{
					"m-light": 16.0,
					"m-dark":  16.0,
				},
			},
			"v-float-opacity": {
				ID:                   "v-float-opacity",
				Name:                 "Opacity/Disabled",
				VariableCollectionID: "col-1",
				ResolvedType:         "FLOAT",
				Scopes:               []string{"OPACITY"},
				ValuesByMode: map[string]any{
					"m-light": 0.38,
					"m-dark":  0.38,
				},
			},
			"v-bool": {
				ID:                   "v-bool",
				Name:                 "Features/DarkModeDefault",
				VariableCollectionID: "col-1",
				ResolvedType:         "BOOLEAN",
				ValuesByMode: map[string]any{
					"m-light": false,
					"m-dark":  true,
				},
			},
		},
	}

	tokens, err := ProcessVariables(meta)
	if err != nil {
		t.Fatalf("ProcessVariables failed: %v", err)
	}

	if len(tokens.Collections) != 1 {
		t.Fatalf("expected 1 collection, got %d", len(tokens.Collections))
	}

	col := tokens.Collections[0]
	tokenMap := make(map[string]ResolvedToken)
	for _, tk := range col.Tokens {
		tokenMap[tk.Name] = tk
	}

	// Verify alias resolution for Colors/Action -> Colors/Primary
	actionToken, ok := tokenMap["Colors/Action"]
	if !ok {
		t.Fatalf("Colors/Action token not found")
	}
	if actionToken.Values["Light"] != "#3366CC" {
		t.Errorf("expected Light alias resolved to #3366CC, got %s", actionToken.Values["Light"])
	}
	if actionToken.Values["Dark"] != "#6699FF" {
		t.Errorf("expected Dark alias resolved to #6699FF, got %s", actionToken.Values["Dark"])
	}

	// Verify Spacing float -> 16px
	spaceToken := tokenMap["Spacing/4"]
	if spaceToken.Values["Light"] != "16px" {
		t.Errorf("expected Spacing 16px, got %s", spaceToken.Values["Light"])
	}

	// Verify Opacity float -> 0.38 unitless
	opacityToken := tokenMap["Opacity/Disabled"]
	if opacityToken.Values["Light"] != "0.38" {
		t.Errorf("expected Opacity 0.38, got %s", opacityToken.Values["Light"])
	}

	// Verify Boolean
	boolToken := tokenMap["Features/DarkModeDefault"]
	if boolToken.Values["Light"] != "false" || boolToken.Values["Dark"] != "true" {
		t.Errorf("unexpected bool token values: %+v", boolToken.Values)
	}

	// Verify Cycle terminates without crash
	loop1Token := tokenMap["Loop1"]
	if loop1Token.Values["Light"] == "" {
		t.Errorf("expected loop token to have value")
	}
}

func TestExportCSS(t *testing.T) {
	meta := &figma.VariablesMeta{
		VariableCollections: map[string]figma.VariableCollection{
			"col-theme": {
				ID:            "col-theme",
				Name:          "Theme",
				DefaultModeID: "m-light",
				Modes: []figma.VariableMode{
					{ModeID: "m-light", Name: "Light"},
					{ModeID: "m-dark", Name: "Dark"},
				},
			},
		},
		Variables: map[string]figma.Variable{
			"v1": {
				ID:                   "v1",
				Name:                 "Colors/Bg",
				VariableCollectionID: "col-theme",
				ResolvedType:         "COLOR",
				ValuesByMode: map[string]any{
					"m-light": map[string]any{"r": 1.0, "g": 1.0, "b": 1.0, "a": 1.0},
					"m-dark":  map[string]any{"r": 0.1, "g": 0.1, "b": 0.1, "a": 1.0},
				},
			},
		},
	}

	css, err := Export(meta, FormatCSS)
	if err != nil {
		t.Fatalf("Export(CSS) failed: %v", err)
	}

	if !strings.Contains(css, ":root {") {
		t.Errorf("missing :root block in CSS:\n%s", css)
	}
	if !strings.Contains(css, "--colors-bg: #FFFFFF;") {
		t.Errorf("missing --colors-bg in :root in CSS:\n%s", css)
	}
	if !strings.Contains(css, ".dark {") {
		t.Errorf("missing .dark block in CSS:\n%s", css)
	}
	if !strings.Contains(css, "--colors-bg: #1A1A1A;") {
		t.Errorf("missing dark --colors-bg in CSS:\n%s", css)
	}
}

func TestExportTailwindAndJSON(t *testing.T) {
	meta := &figma.VariablesMeta{
		VariableCollections: map[string]figma.VariableCollection{
			"col-1": {
				ID:            "col-1",
				Name:          "Primitives",
				DefaultModeID: "m1",
				Modes:         []figma.VariableMode{{ModeID: "m1", Name: "Default"}},
			},
		},
		Variables: map[string]figma.Variable{
			"v-col": {
				ID:                   "v-col",
				Name:                 "Colors/Blue/500",
				VariableCollectionID: "col-1",
				ResolvedType:         "COLOR",
				ValuesByMode:         map[string]any{"m1": map[string]any{"r": 0.23, "g": 0.51, "b": 0.96, "a": 1.0}},
			},
			"v-sp": {
				ID:                   "v-sp",
				Name:                 "Spacing/4",
				VariableCollectionID: "col-1",
				ResolvedType:         "FLOAT",
				ValuesByMode:         map[string]any{"m1": 16.0},
			},
			"v-rad": {
				ID:                   "v-rad",
				Name:                 "Radius/lg",
				VariableCollectionID: "col-1",
				ResolvedType:         "FLOAT",
				ValuesByMode:         map[string]any{"m1": 8.0},
			},
		},
	}

	tw, err := Export(meta, FormatTailwind)
	if err != nil {
		t.Fatalf("Export(Tailwind) failed: %v", err)
	}

	if !strings.Contains(tw, "module.exports = {") {
		t.Errorf("missing module.exports in Tailwind:\n%s", tw)
	}
	if !strings.Contains(tw, "colors: {") || !strings.Contains(tw, "'blue-500': '#3B82F5'") {
		t.Errorf("missing colors in Tailwind:\n%s", tw)
	}
	if !strings.Contains(tw, "spacing: {") || !strings.Contains(tw, "'4': '16px'") {
		t.Errorf("missing spacing in Tailwind:\n%s", tw)
	}
	if !strings.Contains(tw, "borderRadius: {") || !strings.Contains(tw, "'lg': '8px'") {
		t.Errorf("missing borderRadius in Tailwind:\n%s", tw)
	}

	jsonStr, err := Export(meta, FormatJSON)
	if err != nil {
		t.Fatalf("Export(JSON) failed: %v", err)
	}
	if !strings.Contains(jsonStr, `"name": "Primitives"`) || !strings.Contains(jsonStr, `"cssName": "--colors-blue-500"`) {
		t.Errorf("unexpected JSON export:\n%s", jsonStr)
	}

	// Test unsupported format
	if _, err := Export(meta, "invalid"); err == nil {
		t.Fatal("expected error for invalid format")
	}
}
