package serialize

import (
	"strings"
	"testing"

	"github.com/ducconit/figma-cli/internal/extractors"
)

func TestSerializeTreeYAMLJSONAndStripNoiseNames(t *testing.T) {
	boldW := 700
	design := &extractors.SimplifiedDesign{
		Name: "Checkout UI",
		GlobalVars: extractors.GlobalVars{
			Styles: map[string]any{
				"fill_ffffff": []any{"#FFFFFF"},
			},
		},
		Elements: map[string]extractors.ElementBody{
			"EL-12345678": {
				"type":  "TEXT",
				"fills": "fill_ffffff",
			},
		},
		Nodes: []*extractors.SimplifiedNode{
			{
				ID:           "10:1",
				Name:         "Frame 123", // Auto-generated noise name -> should be stripped
				Type:         "FRAME",
				BorderRadius: "12px",
				Fills:        "fill_ffffff",
				Children: []*extractors.SimplifiedNode{
					{
						ID:         "10:2",
						Name:       "Stale Text Layer Name", // TEXT node -> name must be stripped even with template
						Template:   "EL-12345678",
						Text:       "Pay Now",
						BoldWeight: &boldW,
					},
					{
						ID:   "10:3",
						Name: "SubmitButton", // Meaningful name -> preserved
						Type: "INSTANCE",
					},
				},
			},
		},
	}

	treeOut, err := Serialize(design, "tree")
	if err != nil {
		t.Fatalf("unexpected tree serialize error: %v", err)
	}

	if strings.Contains(treeOut, "Frame 123") {
		t.Fatalf("expected auto-generated 'Frame 123' to be stripped from tree output:\n%s", treeOut)
	}
	if strings.Contains(treeOut, "Stale Text Layer Name") {
		t.Fatalf("expected TEXT node name to be stripped even when templated:\n%s", treeOut)
	}
	if !strings.Contains(treeOut, `[INSTANCE] "SubmitButton" #10:3`) {
		t.Fatalf("expected meaningful name 'SubmitButton' to be preserved:\n%s", treeOut)
	}
	if !strings.Contains(treeOut, `[TEXT] #10:2 template=EL-12345678 boldWeight=700 text="Pay Now"`) {
		t.Fatalf("expected compact 1-line TEXT node in tree output:\n%s", treeOut)
	}

	yamlOut, err := Serialize(design, "yaml")
	if err != nil || !strings.Contains(yamlOut, "Checkout UI") {
		t.Fatalf("unexpected yaml output: %v\n%s", err, yamlOut)
	}

	jsonOut, err := Serialize(design, "json")
	if err != nil || !strings.Contains(jsonOut, `"Checkout UI"`) {
		t.Fatalf("unexpected json output: %v\n%s", err, jsonOut)
	}
}
