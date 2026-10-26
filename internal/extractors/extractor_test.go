package extractors

import (
	"fmt"
	"testing"
)

func TestSimplifyRawFigmaObjectCollapseAndFinalize(t *testing.T) {
	rawResponse := map[string]any{
		"name": "Test App",
		"styles": map[string]any{
			"S:named1": map[string]any{
				"name": "Brand / Primary",
			},
		},
		"document": map[string]any{
			"id":   "0:0",
			"type": "DOCUMENT",
			"children": []any{
				map[string]any{
					"id":           "1:0",
					"name":         "Main Screen",
					"type":         "FRAME",
					"clipsContent": true,
					"layoutMode":   "VERTICAL",
					"itemSpacing":  12.0,
					"styles": map[string]any{
						"fill": "S:named1",
					},
					"fills": []any{
						map[string]any{
							"type":  "SOLID",
							"color": map[string]any{"r": 0.1, "g": 0.2, "b": 0.8, "a": 1.0},
						},
					},
					"children": []any{
						// 1. Vector icon group -> should collapse into IMAGE-SVG with empty children
						map[string]any{
							"id":   "1:1",
							"name": "ic-bell",
							"type": "GROUP",
							"children": []any{
								map[string]any{"id": "1:2", "name": "Vector 1", "type": "VECTOR"},
								map[string]any{"id": "1:3", "name": "Ellipse 1", "type": "ELLIPSE"},
							},
						},
						// 2. Two identical Rectangle items -> should deduplicate into EL-xxxxxxxx and inline exclusive fill
						map[string]any{
							"id":           "1:4",
							"name":         "Badge A",
							"type":         "RECTANGLE",
							"cornerRadius": 8.0,
							"fills": []any{
								map[string]any{
									"type":  "SOLID",
									"color": map[string]any{"r": 1.0, "g": 0.0, "b": 0.0, "a": 1.0},
								},
							},
						},
						map[string]any{
							"id":           "1:5",
							"name":         "Badge B",
							"type":         "RECTANGLE",
							"cornerRadius": 8.0,
							"fills": []any{
								map[string]any{
									"type":  "SOLID",
									"color": map[string]any{"r": 1.0, "g": 0.0, "b": 0.0, "a": 1.0},
								},
							},
						},
					},
				},
			},
		},
	}

	design, err := SimplifyRawFigmaObject(rawResponse, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if design.Name != "Test App" || len(design.Nodes) != 1 {
		t.Fatalf("unexpected design root: %+v", design)
	}
	root := design.Nodes[0]

	// Named style "Brand / Primary" must be preserved in GlobalVars even though used once
	if root.Fills != "Brand / Primary" {
		t.Fatalf("expected named style 'Brand / Primary' on root, got %v", root.Fills)
	}
	if _, exists := design.GlobalVars.Styles["Brand / Primary"]; !exists {
		t.Fatalf("expected 'Brand / Primary' in GlobalVars.Styles: %+v", design.GlobalVars.Styles)
	}

	// Single-use generated layout on root must be inlined as a map, not a string ref
	if _, isMap := root.Layout.(map[string]any); !isMap {
		t.Fatalf("expected single-use root layout to be inlined as map, got %T (%v)", root.Layout, root.Layout)
	}

	// First child "ic-bell" must be collapsed into IMAGE-SVG with zero children
	iconNode := root.Children[0]
	if iconNode.Type != "IMAGE-SVG" || len(iconNode.Children) != 0 {
		t.Fatalf("expected ic-bell to collapse into IMAGE-SVG leaf, got type=%s children=%d", iconNode.Type, len(iconNode.Children))
	}

	// Second and third children must share an EL-xxxxxxxx template
	badgeA := root.Children[1]
	badgeB := root.Children[2]
	if badgeA.Template == "" || badgeA.Template != badgeB.Template {
		t.Fatalf("expected Badge A and Badge B to share an EL-xxxxxxxx template, got %q and %q", badgeA.Template, badgeB.Template)
	}
	elBody, exists := design.Elements[badgeA.Template]
	if !exists || elBody["borderRadius"] != "8px" {
		t.Fatalf("expected template %s in design.Elements, got %+v", badgeA.Template, elBody)
	}
	// Exclusive fill on the template should be inlined inside the element body
	if _, isSlice := elBody["fills"].([]any); !isSlice {
		t.Fatalf("expected exclusive fill on template to be inlined, got %T (%v)", elBody["fills"], elBody["fills"])
	}
}

func TestConcurrentMultiFrameExtractionDeterminism(t *testing.T) {
	var topFrames []any
	for f := 1; f <= 6; f++ {
		var children []any
		for c := 1; c <= 4; c++ {
			children = append(children, map[string]any{
				"id":           fmt.Sprintf("%d:%d", f, c),
				"name":         fmt.Sprintf("Card %d-%d", f, c),
				"type":         "RECTANGLE",
				"cornerRadius": 12.0,
				"fills": []any{
					map[string]any{
						"type":  "SOLID",
						"color": map[string]any{"r": 0.2, "g": 0.4, "b": 0.6, "a": 1.0},
					},
				},
			})
		}
		topFrames = append(topFrames, map[string]any{
			"id":         fmt.Sprintf("%d:0", f),
			"name":       fmt.Sprintf("Screen %d", f),
			"type":       "FRAME",
			"layoutMode": "HORIZONTAL",
			"children":   children,
		})
	}

	raw := map[string]any{
		"name": "Concurrent Design",
		"document": map[string]any{
			"id":       "0:0",
			"type":     "DOCUMENT",
			"children": topFrames,
		},
	}

	first, err := SimplifyRawFigmaObject(raw, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(first.Nodes) != 6 {
		t.Fatalf("expected 6 top-level screens, got %d", len(first.Nodes))
	}

	for run := 0; run < 5; run++ {
		next, err := SimplifyRawFigmaObject(raw, Options{})
		if err != nil {
			t.Fatalf("run %d failed: %v", run, err)
		}
		for i := range first.Nodes {
			if next.Nodes[i].ID != first.Nodes[i].ID || next.Nodes[i].Template != first.Nodes[i].Template {
				t.Fatalf("non-deterministic top node at %d: %+v vs %+v", i, first.Nodes[i], next.Nodes[i])
			}
			for j := range first.Nodes[i].Children {
				if next.Nodes[i].Children[j].Template == "" || next.Nodes[i].Children[j].Template != first.Nodes[i].Children[j].Template {
					t.Fatalf("non-deterministic child template at [%d][%d]", i, j)
				}
			}
		}
	}
}

func TestExtractorComponentsRichTextAndGetFileNodesResponse(t *testing.T) {
	rawNodesResp := map[string]any{
		"name": "Component System",
		"nodes": map[string]any{
			"20:1": map[string]any{
				"components": map[string]any{
					"20:2": map[string]any{
						"key":            "comp_key_1",
						"name":           "PrimaryButton",
						"componentSetId": "20:1",
						"description":    "Main CTA button",
					},
				},
				"componentSets": map[string]any{
					"20:1": map[string]any{
						"key":         "set_key_1",
						"name":        "ButtonSet",
						"description": "Button variants",
					},
				},
				"document": map[string]any{
					"id":   "20:1",
					"name": "ButtonSet",
					"type": "COMPONENT_SET",
					"componentPropertyDefinitions": map[string]any{
						"ShowIcon#1:0": map[string]any{"type": "BOOLEAN", "defaultValue": true},
					},
					"children": []any{
						map[string]any{
							"id":      "20:2",
							"name":    "PrimaryButton",
							"type":    "COMPONENT",
							"opacity": 0.85,
							"rectangleCornerRadii": []any{4.0, 8.0, 4.0, 8.0},
							"strokes": []any{
								map[string]any{
									"type":  "SOLID",
									"color": map[string]any{"r": 0.0, "g": 0.0, "b": 0.0, "a": 1.0},
								},
							},
							"strokeWeight": 1.5,
							"effects": []any{
								map[string]any{
									"type":    "LAYER_BLUR",
									"visible": true,
									"radius":  4.0,
								},
							},
							"children": []any{
								// Hidden child rescued because it is inside COMPONENT_SET/COMPONENT and has visible property reference
								map[string]any{
									"id":      "20:3",
									"name":    "RescuedIcon",
									"type":    "VECTOR",
									"visible": false,
									"componentPropertyReferences": map[string]any{
										"visible": "ShowIcon#1:0",
									},
								},
								// Normal hidden child without property ref -> skipped
								map[string]any{
									"id":      "20:4",
									"name":    "SkippedHidden",
									"type":    "RECTANGLE",
									"visible": false,
								},
								// Rich text child triggering registerInlineTextStyle (ts1)
								map[string]any{
									"id":         "20:5",
									"name":       "CTA Label",
									"type":       "TEXT",
									"characters": "Click Here",
									"style": map[string]any{
										"fontFamily": "Inter",
										"fontSize":   14.0,
									},
									"characterStyleOverrides": []any{1.0, 1.0, 1.0, 1.0, 1.0, 0.0, 0.0, 0.0, 0.0, 0.0},
									"styleOverrideTable": map[string]any{
										"1": map[string]any{
											"fills": []any{
												map[string]any{
													"type":  "SOLID",
													"color": map[string]any{"r": 1.0, "g": 1.0, "b": 0.0, "a": 1.0},
												},
											},
										},
									},
								},
								// Instance child
								map[string]any{
									"id":          "20:6",
									"name":        "NestedInstance",
									"type":        "INSTANCE",
									"componentId": "20:2",
									"componentProperties": map[string]any{
										"ShowIcon#1:0": map[string]any{"type": "BOOLEAN", "value": false},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	design, err := SimplifyRawFigmaObject(rawNodesResp, Options{})
	if err != nil {
		t.Fatalf("SimplifyRawFigmaObject failed: %v", err)
	}
	if len(design.Components) != 1 || len(design.ComponentSets) != 1 {
		t.Fatalf("expected 1 component and 1 componentSet, got %d and %d", len(design.Components), len(design.ComponentSets))
	}
	compNode := design.Nodes[0].Children[0]
	if compNode.BorderRadius != "4px 8px 4px 8px" || compNode.StrokeWeight != "1.5px" || compNode.Opacity == nil || *compNode.Opacity != 0.85 {
		t.Fatalf("unexpected visuals on component node: %+v", compNode)
	}
	// RescuedIcon + CTA Label + NestedInstance = 3 children (SkippedHidden was filtered out)
	if len(compNode.Children) != 3 {
		t.Fatalf("expected 3 children (hidden child with visible ref rescued, normal hidden skipped), got %d", len(compNode.Children))
	}
	if _, hasTS1 := design.GlobalVars.Styles["ts1"]; !hasTS1 {
		t.Fatalf("expected ts1 inline rich-text style in GlobalVars.Styles: %+v", design.GlobalVars.Styles)
	}
}
