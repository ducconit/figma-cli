package motion

import (
	"strings"
	"testing"
)

func TestExtractAndExportMotion(t *testing.T) {
	rawFigma := map[string]any{
		"name": "Interactive Prototype",
		"document": map[string]any{
			"id":   "0:0",
			"name": "Document",
			"type": "DOCUMENT",
			"children": []any{
				map[string]any{
					"id":   "1:1",
					"name": "Page 1",
					"type": "CANVAS",
					"children": []any{
						// Node 1: Legacy transition properties with EASE_IN_AND_OUT
						map[string]any{
							"id":                 "10:1",
							"name":               "PrimaryButton",
							"type":               "FRAME",
							"transitionNodeID":   "10:2",
							"transitionDuration": 0.25,
							"transitionEasing":   "EASE_IN_AND_OUT",
						},
						// Node 2: Modern interactions array with CUSTOM_CUBIC_BEZIER and SMART_ANIMATE
						map[string]any{
							"id":   "10:2",
							"name": "ModalDialog",
							"type": "FRAME",
							"interactions": []any{
								map[string]any{
									"trigger": map[string]any{
										"type": "ON_CLICK",
									},
									"actions": []any{
										map[string]any{
											"type":          "NODE",
											"destinationId": "10:3",
											"navigation":    "NAVIGATE",
											"transition": map[string]any{
												"type":      "SMART_ANIMATE",
												"duration":  0.4,
												"direction": "BOTTOM",
												"easing": map[string]any{
													"type": "CUSTOM_CUBIC_BEZIER",
													"easingFunctionCubicBezier": map[string]any{
														"x1": 0.22,
														"y1": 1.0,
														"x2": 0.36,
														"y2": 1.0,
													},
												},
											},
										},
									},
								},
							},
						},
						// Node 3: Partial / missing custom cubic bezier (Review Focus boundary case)
						map[string]any{
							"id":                 "10:3",
							"name":               "ToastBanner",
							"type":               "FRAME",
							"transitionNodeID":   "10:1",
							"transitionEasing":   "CUSTOM_CUBIC_BEZIER",
							// missing transitionDuration and easingFunctionCubicBezier -> should fallback safely to 300ms and standard ease
						},
					},
				},
			},
		},
	}

	report, err := ExtractMotion(rawFigma)
	if err != nil {
		t.Fatalf("ExtractMotion failed: %v", err)
	}

	if report.FileName != "Interactive Prototype" {
		t.Errorf("expected FileName 'Interactive Prototype', got %q", report.FileName)
	}
	if len(report.Transitions) != 3 {
		t.Fatalf("expected 3 transitions, got %d: %+v", len(report.Transitions), report.Transitions)
	}

	// Verify Node 1 (PrimaryButton)
	t1 := report.Transitions[0]
	if t1.NodeID != "10:1" || t1.DurationMs != 250 || t1.EasingType != "EASE_IN_AND_OUT" {
		t.Errorf("unexpected t1: %+v", t1)
	}

	// Verify Node 2 (ModalDialog)
	t2 := report.Transitions[1]
	if t2.NodeID != "10:2" || t2.Trigger != "ON_CLICK" || t2.TransitionType != "SMART_ANIMATE" || t2.DurationMs != 400 {
		t.Errorf("unexpected t2: %+v", t2)
	}
	if t2.CubicBezier != [4]float64{0.22, 1.0, 0.36, 1.0} {
		t.Errorf("unexpected t2 CubicBezier: %+v", t2.CubicBezier)
	}

	// Verify Node 3 fallback safety
	t3 := report.Transitions[2]
	if t3.NodeID != "10:3" || t3.DurationMs != 300 {
		t.Errorf("expected fallback 300ms on t3, got %+v", t3)
	}

	// Test CSS export
	cssOut, err := Export(report, FormatCSS)
	if err != nil {
		t.Fatalf("Export(FormatCSS) failed: %v", err)
	}
	if !strings.Contains(cssOut, "cubic-bezier(0.22, 1, 0.36, 1)") || !strings.Contains(cssOut, "250ms") || !strings.Contains(cssOut, "@keyframes") {
		t.Errorf("unexpected CSS export:\n%s", cssOut)
	}

	// Test Framer Motion export
	framerOut, err := Export(report, FormatFramer)
	if err != nil {
		t.Fatalf("Export(FormatFramer) failed: %v", err)
	}
	if !strings.Contains(framerOut, "ease: [0.22, 1, 0.36, 1]") || !strings.Contains(framerOut, "duration: 0.4") || !strings.Contains(framerOut, "PrimaryButton") {
		t.Errorf("unexpected Framer Motion export:\n%s", framerOut)
	}

	// Test JSON export
	jsonOut, err := Export(report, FormatJSON)
	if err != nil {
		t.Fatalf("Export(FormatJSON) failed: %v", err)
	}
	if !strings.Contains(jsonOut, `"nodeId": "10:2"`) || !strings.Contains(jsonOut, `"transitionType": "SMART_ANIMATE"`) {
		t.Errorf("unexpected JSON export:\n%s", jsonOut)
	}

	// Test ParseFormat
	if f, err := ParseFormat("motion"); err != nil || f != FormatFramer {
		t.Errorf("expected FormatFramer for 'motion', got %q (err: %v)", f, err)
	}
	if _, err := ParseFormat("invalid-format"); err == nil {
		t.Error("expected error for invalid motion format")
	}

	// Verify 3+ colliding node names in ToFramer produce unique keys ("card", "card-2", "card-3")
	collidingReport := &MotionReport{
		FileName: "Duplicates",
		Transitions: []TransitionSpec{
			{NodeID: "1:1", NodeName: "Card", DurationMs: 200},
			{NodeID: "1:2", NodeName: "Card", DurationMs: 300},
			{NodeID: "1:3", NodeName: "Card", DurationMs: 400},
		},
	}
	framerDup := collidingReport.ToFramer()
	if !strings.Contains(framerDup, `"card":`) || !strings.Contains(framerDup, `"card-2":`) || !strings.Contains(framerDup, `"card-3":`) {
		t.Errorf("expected card, card-2, and card-3 in Framer output:\n%s", framerDup)
	}
}

func TestMotionCommentSanitization(t *testing.T) {
	report := &MotionReport{
		FileName: "Malicious*/body { background: red; }/*",
		Transitions: []TransitionSpec{
			{
				NodeID:         "1:1",
				NodeName:       "Card\nimport evil from 'evil';\n//",
				Trigger:        "ON_CLICK*/",
				TransitionType: "SMART_ANIMATE\n",
				DurationMs:     200,
			},
		},
	}

	css := report.ToCSS()
	if strings.Contains(css, "*/body") {
		t.Fatalf("CSS block comment escape succeeded:\n%s", css)
	}

	framer := report.ToFramer()
	lines := strings.Split(framer, "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "import evil") {
			t.Fatalf("Framer comment breakout succeeded:\n%s", framer)
		}
	}
}
