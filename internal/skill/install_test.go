package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallDefaultHomePathAndAgentSymlinks(t *testing.T) {
	fakeHome := t.TempDir()
	fakeBinary := filepath.Join(t.TempDir(), "fm-source-bin")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\necho fm\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Default install (no Path, default SkillName="figma-cli") -> ~/.agents/skills/figma-cli + symlinks for agy, claude, codex
	report, err := Install(InstallOptions{
		HomeDir:       fakeHome,
		Token:         "figd_installed_secret_1234",
		BinarySrcPath: fakeBinary,
		TargetOS:      "linux",
		Agents:        []string{"agy", "claude", "codex"},
	})
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	expectedCanonical := filepath.Join(fakeHome, ".agents", "skills", "figma-cli")
	if report.SkillDir != expectedCanonical {
		t.Fatalf("expected default SkillDir %q, got %q", expectedCanonical, report.SkillDir)
	}

	// Verify SKILL.md and references/tree-dsl.md exist
	skillBytes, err := os.ReadFile(report.SkillMDPath)
	if err != nil {
		t.Fatalf("expected SKILL.md at %s: %v", report.SkillMDPath, err)
	}
	if !strings.Contains(string(skillBytes), "name: figma-cli") || !strings.Contains(string(skillBytes), "scripts/fm") {
		t.Fatalf("unexpected SKILL.md content:\n%s", string(skillBytes))
	}
	if _, err := os.Stat(report.ReferenceMDPath); err != nil {
		t.Fatalf("expected tree-dsl.md at %s: %v", report.ReferenceMDPath, err)
	}

	// Verify binary copied to scripts/fm with executable permissions
	info, err := os.Stat(report.BinaryDestPath)
	if err != nil {
		t.Fatalf("expected binary at %s: %v", report.BinaryDestPath, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected executable permissions on %s, got %v", report.BinaryDestPath, info.Mode())
	}

	// Verify .env file contains FIGMA_API_KEY
	envBytes, err := os.ReadFile(report.EnvFilePath)
	if err != nil {
		t.Fatalf("expected .env file at %s: %v", report.EnvFilePath, err)
	}
	if !strings.Contains(string(envBytes), "FIGMA_API_KEY=figd_installed_secret_1234") {
		t.Fatalf("expected FIGMA_API_KEY in .env, got:\n%s", string(envBytes))
	}

	// Verify symlinks for antigravity (~/.gemini/config/skills/figma-cli), claude (~/.claude/skills/figma-cli), codex (~/.codex/skills/figma-cli)
	if len(report.Symlinks) != 3 {
		t.Fatalf("expected 3 symlinks, got %d: %+v", len(report.Symlinks), report.Symlinks)
	}
	expectedLinks := map[string]string{
		"antigravity": filepath.Join(fakeHome, ".gemini", "config", "skills", "figma-cli"),
		"claude":      filepath.Join(fakeHome, ".claude", "skills", "figma-cli"),
		"codex":       filepath.Join(fakeHome, ".codex", "skills", "figma-cli"),
	}
	for _, link := range report.Symlinks {
		wantPath, ok := expectedLinks[link.Agent]
		if !ok {
			t.Fatalf("unexpected agent in symlinks: %+v", link)
		}
		if link.LinkPath != wantPath {
			t.Fatalf("for agent %s expected LinkPath %q, got %q", link.Agent, wantPath, link.LinkPath)
		}
		// Ensure SKILL.md is accessible via the symlink
		linkedSkillMD := filepath.Join(link.LinkPath, "SKILL.md")
		if _, err := os.Stat(linkedSkillMD); err != nil {
			t.Fatalf("expected SKILL.md readable through symlink %s: %v", linkedSkillMD, err)
		}
	}

	// 2. Re-run with --all to verify idempotency and all agent targets
	reportAll, err := Install(InstallOptions{
		HomeDir:       fakeHome,
		BinarySrcPath: fakeBinary,
		TargetOS:      "linux",
		Agents:        []string{"all"},
	})
	if err != nil {
		t.Fatalf("re-run with all agents failed: %v", err)
	}
	if len(reportAll.Symlinks) != len(SupportedAgentTargets) {
		t.Fatalf("expected %d symlinks for 'all', got %d", len(SupportedAgentTargets), len(reportAll.Symlinks))
	}
}

func TestInstallCustomProjectPathAndWindowsBinary(t *testing.T) {
	fakeHome := t.TempDir()
	projectDir := t.TempDir()
	fakeBinary := filepath.Join(t.TempDir(), "fm-source-bin")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\necho fm\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Project install using --path .agent/skills (resolved under projectDir)
	customSkillsPath := filepath.Join(projectDir, ".agent", "skills")
	winReport, err := Install(InstallOptions{
		Path:          customSkillsPath,
		HomeDir:       fakeHome,
		BinarySrcPath: fakeBinary,
		TargetOS:      "windows",
		Agents:        []string{"antigravity"},
	})
	if err != nil {
		t.Fatalf("Project path Install failed: %v", err)
	}
	wantSkillDir := filepath.Join(projectDir, ".agent", "skills", "figma-cli")
	if winReport.SkillDir != wantSkillDir {
		t.Fatalf("expected SkillDir %q, got %q", wantSkillDir, winReport.SkillDir)
	}
	if !strings.HasSuffix(winReport.BinaryDestPath, "fm.exe") {
		t.Fatalf("expected fm.exe for Windows target, got %s", winReport.BinaryDestPath)
	}
	if len(winReport.Symlinks) != 1 || winReport.Symlinks[0].TargetPath != wantSkillDir {
		t.Fatalf("expected symlink pointing to %q, got %+v", wantSkillDir, winReport.Symlinks)
	}

	// Legacy TargetDir + copyDirectoryRecursive + direct symlink mode + unsupported agent error
	legacyDir := t.TempDir()
	legReport, err := Install(InstallOptions{
		TargetDir:     legacyDir,
		HomeDir:       fakeHome,
		BinarySrcPath: fakeBinary,
		TargetOS:      "linux",
		Global:        true,
	})
	if err != nil || legReport.SkillDir != filepath.Join(legacyDir, ".agents", "skills", "figma-cli") {
		t.Fatalf("unexpected legacy TargetDir report: %+v (err=%v)", legReport, err)
	}

	copyDest := filepath.Join(t.TempDir(), "copied-skill")
	if err := copyDirectoryRecursive(wantSkillDir, copyDest); err != nil {
		t.Fatalf("copyDirectoryRecursive failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(copyDest, "SKILL.md")); err != nil {
		t.Fatalf("expected SKILL.md in copied directory: %v", err)
	}

	directLink, err := InstallAgentSymlink("direct-agent", wantSkillDir, wantSkillDir)
	if err != nil || directLink.Mode != "direct" {
		t.Fatalf("expected Mode=direct when source and dest match, got %+v (err=%v)", directLink, err)
	}

	if _, err := Install(InstallOptions{HomeDir: fakeHome, BinarySrcPath: fakeBinary, Agents: []string{"unknown-agent"}}); err == nil {
		t.Fatal("expected error for unsupported agent target")
	}
}
