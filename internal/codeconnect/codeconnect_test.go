package codeconnect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ducconit/figma-cli/internal/extractors"
)

func TestScanMatchAndGenerateCodeConnect(t *testing.T) {
	srcDir := t.TempDir()
	compDir := filepath.Join(srcDir, "components")
	if err := os.MkdirAll(compDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create sample React, Vue, and SwiftUI component files
	if err := os.WriteFile(filepath.Join(compDir, "PrimaryButton.tsx"), []byte(`
export interface PrimaryButtonProps {
  label: string;
  disabled?: boolean;
  variant?: "solid" | "outline";
}
export function PrimaryButton({ label, disabled, variant }: PrimaryButtonProps) {
  return <button disabled={disabled}>{label}</button>;
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(compDir, "UserCard.vue"), []byte(`<template><div class="card"></div></template>`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(compDir, "ProfileAvatar.swift"), []byte(`import SwiftUI
struct ProfileAvatar: View { var body: some View { EmptyView() } }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	locals, err := ScanLocalComponents(srcDir)
	if err != nil {
		t.Fatalf("ScanLocalComponents failed: %v", err)
	}
	if len(locals) != 3 {
		t.Fatalf("expected 3 local components, got %d: %+v", len(locals), locals)
	}

	design := &extractors.SimplifiedDesign{
		Name: "Design System",
		ComponentSets: map[string]extractors.SimplifiedComponentSetDefinition{
			"100:1": {
				ID:   "100:1",
				Key:  "cs_btn",
				Name: "Buttons / Primary Button",
				PropertyDefinitions: map[string]any{
					"Label":    map[string]any{"type": "TEXT", "defaultValue": "Click me"},
					"Disabled": map[string]any{"type": "BOOLEAN", "defaultValue": false},
					"Variant":  map[string]any{"type": "VARIANT", "defaultValue": "solid"},
				},
			},
		},
		Components: map[string]extractors.SimplifiedComponentDefinition{
			"100:2": {
				ID:             "100:2",
				Key:            "c_btn_default",
				Name:           "Variant=solid",
				ComponentSetID: "100:1",
			},
			"200:1": {
				ID:   "200:1",
				Key:  "c_card",
				Name: "Cards/UserCard",
			},
			"300:1": {
				ID:   "300:1",
				Key:  "c_unmapped",
				Name: "Navigation/SidebarDrawer",
			},
		},
	}

	matches := MatchComponents(design, locals, "abc123XYZ")
	if len(matches) != 3 {
		t.Fatalf("expected 3 top-level component matches (excluding variant child 100:2), got %d: %+v", len(matches), matches)
	}

	// Verify Buttons / Primary Button matched PrimaryButton.tsx
	var btnMatch *ComponentMatch
	for i := range matches {
		if matches[i].FigmaID == "100:1" {
			btnMatch = &matches[i]
			break
		}
	}
	if btnMatch == nil || btnMatch.Local == nil || btnMatch.Local.Name != "PrimaryButton" {
		t.Fatalf("expected 100:1 to match PrimaryButton, got %+v", btnMatch)
	}

	tsCode := GenerateFigmaTS(*btnMatch, "abc123XYZ")
	if !strings.Contains(tsCode, "figma.code`") || !strings.Contains(tsCode, "node-id=100-1") || !strings.Contains(tsCode, "<PrimaryButton") {
		t.Errorf("unexpected .figma.ts output:\n%s", tsCode)
	}
}

func TestCodeConnectPathSanitization(t *testing.T) {
	outDir := t.TempDir()
	maliciousMatches := []ComponentMatch{
		{
			FigmaID:   "999:1",
			FigmaName: "../../etc/passwd",
			Local: &LocalComponent{
				Name:      "../../etc/passwd",
				RelPath:   "components/Evil.tsx",
				Framework: "react",
			},
		},
	}

	written, err := WriteTemplates(maliciousMatches, "abc123XYZ", outDir)
	if err != nil {
		t.Fatalf("WriteTemplates failed: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 written file, got %d", len(written))
	}

	absOutDir, _ := filepath.Abs(outDir)
	absWritten, _ := filepath.Abs(written[0])
	if !strings.HasPrefix(absWritten, absOutDir+string(filepath.Separator)) {
		t.Fatalf("path traversal detected! written path %q escaped outDir %q", absWritten, absOutDir)
	}

	// Verify 3+ colliding component names write 3 distinct files (Button.figma.ts, Button-2.figma.ts, Button-3.figma.ts)
	dupDir := t.TempDir()
	dupMatches := []ComponentMatch{
		{FigmaID: "1:1", FigmaName: "Button", Local: &LocalComponent{Name: "Button", RelPath: "a/Button.tsx"}},
		{FigmaID: "1:2", FigmaName: "Button", Local: &LocalComponent{Name: "Button", RelPath: "b/Button.tsx"}},
		{FigmaID: "1:3", FigmaName: "Button", Local: &LocalComponent{Name: "Button", RelPath: "c/Button.tsx"}},
	}
	dupWritten, err := WriteTemplates(dupMatches, "abc123XYZ", dupDir)
	if err != nil {
		t.Fatalf("WriteTemplates for duplicates failed: %v", err)
	}
	if len(dupWritten) != 3 || dupWritten[1] == dupWritten[2] {
		t.Fatalf("expected 3 distinct files, got: %v", dupWritten)
	}
}

func TestCodeConnectCommentSanitization(t *testing.T) {
	match := ComponentMatch{
		FigmaID:   "10:1",
		FigmaName: "Button\nimport { execSync } from 'child_process';\n//",
		Local: &LocalComponent{
			Name:    "Button",
			RelPath: "Button.tsx",
		},
		Properties: []string{"Label\nimport evil from 'evil';\n"},
	}

	content := GenerateFigmaTS(match, "file123")
	// Verify that comment line does not have raw newline injection
	lines := strings.Split(content, "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "import { execSync }") {
			t.Fatalf("code injection succeeded! raw import statement found:\n%s", content)
		}
	}
	if !strings.Contains(content, "// Figma Node: Button import { execSync } from 'child_process'; //") {
		t.Errorf("expected newlines sanitized to spaces in comment line:\n%s", content)
	}
}
