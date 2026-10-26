package skill

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ducconit/figma-cli/internal/config"
)

// DefaultSkillsPath is the default canonical directory where agent skills are installed.
const DefaultSkillsPath = "~/.agents/skills"

// DefaultSkillName is the default folder and manifest name for the skill.
const DefaultSkillName = "figma-cli"

// AgentTarget describes a supported AI agent's skill directory convention.
type AgentTarget struct {
	ID      string
	Aliases []string
	RelDir  []string // Path segments under the user's home directory
}

// SupportedAgentTargets lists all built-in AI coding agent skill directories.
var SupportedAgentTargets = []AgentTarget{
	{ID: "antigravity", Aliases: []string{"agy", "gemini"}, RelDir: []string{".gemini", "config", "skills"}},
	{ID: "claude", Aliases: []string{"claude-code"}, RelDir: []string{".claude", "skills"}},
	{ID: "codex", Aliases: []string{"openai"}, RelDir: []string{".codex", "skills"}},
	{ID: "cursor", Aliases: nil, RelDir: []string{".cursor", "skills"}},
	{ID: "windsurf", Aliases: []string{"codeium"}, RelDir: []string{".codeium", "windsurf", "skills"}},
	{ID: "copilot", Aliases: []string{"github-copilot"}, RelDir: []string{".copilot", "skills"}},
	{ID: "opencode", Aliases: nil, RelDir: []string{".config", "opencode", "skills"}},
	{ID: "cline", Aliases: nil, RelDir: []string{".cline", "skills"}},
}

// AgentSymlink records a symlink (or fallback copy) created for a specific AI agent.
type AgentSymlink struct {
	Agent      string
	LinkPath   string
	TargetPath string
	Mode       string // "symlink" or "copy"
}

// InstallOptions configures the cross-platform `fm install` operation.
type InstallOptions struct {
	// Path is the base skills directory (default: ~/.agents/skills; project example: .agent/skills).
	Path          string
	// TargetDir is the legacy project root directory (<TargetDir>/.agents/skills/<SkillName>).
	TargetDir     string
	// HomeDir overrides os.UserHomeDir() when non-empty (useful for isolated testing).
	HomeDir       string
	SkillName     string
	Token         string
	OAuthToken    string
	BinarySrcPath string
	TargetOS      string
	WriteEnvFile  bool
	Global        bool
	// Agents lists AI agent names (e.g. "antigravity", "claude", "codex", "cursor", "all")
	// whose skill directories should be symlinked to the installed canonical skill directory.
	Agents        []string
}

// InstallReport summarizes all paths and symlinks created or updated during `fm install`.
type InstallReport struct {
	OS              string
	Arch            string
	SkillDir        string
	SkillMDPath     string
	ReferenceMDPath string
	BinaryDestPath  string
	EnvFilePath     string
	TokenConfigured bool
	Symlinks        []AgentSymlink
}

// Install installs the universal Figma Agent Skill (SKILL.md, references/tree-dsl.md, and scripts/fm binary)
// into the target skills directory (defaulting to ~/.agents/skills/<skill-name>), creates any requested
// agent-specific symlinks (e.g., --antigravity|--agy -> ~/.gemini/config/skills/<skill-name>, --claude, --codex),
// and optionally saves the Figma access token into .env.
func Install(opts InstallOptions) (*InstallReport, error) {
	targetOS := strings.TrimSpace(opts.TargetOS)
	if targetOS == "" {
		targetOS = runtime.GOOS
	}
	skillName := strings.TrimSpace(opts.SkillName)
	if skillName == "" {
		skillName = DefaultSkillName
	}

	homeDir := strings.TrimSpace(opts.HomeDir)
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home directory: %w", err)
		}
		homeDir = h
	}

	skillDir, envRoots, err := resolveCanonicalSkillDir(opts, homeDir, skillName)
	if err != nil {
		return nil, err
	}

	scriptsDir := filepath.Join(skillDir, "scripts")
	refsDir := filepath.Join(skillDir, "references")

	for _, d := range []string{skillDir, scriptsDir, refsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("create skill directory %s: %w", d, err)
		}
	}

	binName := "fm"
	if strings.EqualFold(targetOS, "windows") {
		binName = "fm.exe"
	}
	binDestPath := filepath.Join(scriptsDir, binName)

	srcBin := strings.TrimSpace(opts.BinarySrcPath)
	if srcBin == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve current executable path: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			srcBin = resolved
		} else {
			srcBin = exe
		}
	}

	binInvocation := formatSkillBinInvocation(skillDir, binDestPath, homeDir)
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	refMDPath := filepath.Join(refsDir, "tree-dsl.md")

	// Resolve requested agent symlinks upfront before parallel I/O
	requestedAgents := append([]string{}, opts.Agents...)
	if opts.Global && len(requestedAgents) == 0 {
		requestedAgents = append(requestedAgents, "antigravity")
	}
	targets, err := resolveAgentTargets(requestedAgents)
	if err != nil {
		return nil, err
	}

	// Phase 1: Concurrently copy executable binary, write SKILL.md, write references/tree-dsl.md, and update .env files
	var (
		fileWG   sync.WaitGroup
		firstErr atomic.Pointer[error]
	)
	recordErr := func(err error) {
		if err != nil {
			e := err
			firstErr.CompareAndSwap(nil, &e)
		}
	}

	fileWG.Add(3)
	go func() {
		defer fileWG.Done()
		if err := copyExecutable(srcBin, binDestPath); err != nil {
			recordErr(fmt.Errorf("install binary to %s: %w", binDestPath, err))
		}
	}()
	go func() {
		defer fileWG.Done()
		if err := os.WriteFile(skillMDPath, []byte(renderSkillMarkdown(skillName, binInvocation)), 0o644); err != nil {
			recordErr(fmt.Errorf("write SKILL.md: %w", err))
		}
	}()
	go func() {
		defer fileWG.Done()
		if err := os.WriteFile(refMDPath, []byte(renderTreeDSLReference()), 0o644); err != nil {
			recordErr(fmt.Errorf("write references/tree-dsl.md: %w", err))
		}
	}()

	report := &InstallReport{
		OS:              targetOS,
		Arch:            runtime.GOARCH,
		SkillDir:        skillDir,
		SkillMDPath:     skillMDPath,
		ReferenceMDPath: refMDPath,
		BinaryDestPath:  binDestPath,
	}

	tok := strings.TrimSpace(opts.Token)
	oauthTok := strings.TrimSpace(opts.OAuthToken)
	if tok != "" || oauthTok != "" || opts.WriteEnvFile {
		homeFigmaEnv := filepath.Join(homeDir, ".figma", ".env")
		envPaths := []string{homeFigmaEnv}
		for _, er := range envRoots {
			candidate := filepath.Join(er, ".env")
			if candidate != homeFigmaEnv {
				envPaths = append(envPaths, candidate)
			}
		}
		for _, ep := range envPaths {
			fileWG.Add(1)
			go func(envPath string) {
				defer fileWG.Done()
				if err := config.UpsertEnvFile(envPath, tok, oauthTok); err != nil {
					recordErr(fmt.Errorf("update env file %s: %w", envPath, err))
				}
			}(ep)
		}
		report.EnvFilePath = homeFigmaEnv
		report.TokenConfigured = tok != "" || oauthTok != ""
	}

	fileWG.Wait()
	if ep := firstErr.Load(); ep != nil {
		return nil, *ep
	}

	// Phase 2: Concurrently create or update agent-specific symlinks via buffered channel
	if len(targets) > 0 {
		type indexedLink struct {
			index int
			link  AgentSymlink
		}
		linkCh := make(chan indexedLink, len(targets))
		var linkWG sync.WaitGroup
		for i, target := range targets {
			linkWG.Add(1)
			go func(idx int, t AgentTarget) {
				defer linkWG.Done()
				parts := append([]string{homeDir}, t.RelDir...)
				parts = append(parts, skillName)
				agentSkillDir := filepath.Join(parts...)

				link, err := InstallAgentSymlink(t.ID, skillDir, agentSkillDir)
				if err != nil {
					recordErr(fmt.Errorf("configure symlink for %s (%s): %w", t.ID, agentSkillDir, err))
					return
				}
				linkCh <- indexedLink{index: idx, link: link}
			}(i, target)
		}
		linkWG.Wait()
		close(linkCh)
		if ep := firstErr.Load(); ep != nil {
			return nil, *ep
		}

		symlinks := make([]AgentSymlink, len(targets))
		for item := range linkCh {
			symlinks[item.index] = item.link
		}
		report.Symlinks = symlinks
	}

	return report, nil
}

// InstallAgentSymlink creates or updates a symbolic link at agentSkillDir pointing to canonicalSkillDir.
// If the OS rejects symlink creation (e.g. unprivileged Windows account), it falls back to copying the directory.
func InstallAgentSymlink(agentID, canonicalSkillDir, agentSkillDir string) (AgentSymlink, error) {
	canonAbs, err := filepath.Abs(canonicalSkillDir)
	if err != nil {
		return AgentSymlink{}, err
	}
	linkAbs, err := filepath.Abs(agentSkillDir)
	if err != nil {
		return AgentSymlink{}, err
	}

	if canonAbs == linkAbs {
		return AgentSymlink{
			Agent:      agentID,
			LinkPath:   linkAbs,
			TargetPath: canonAbs,
			Mode:       "direct",
		}, nil
	}

	parentDir := filepath.Dir(linkAbs)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return AgentSymlink{}, fmt.Errorf("create parent directory %s: %w", parentDir, err)
	}

	// Remove existing symlink or stale directory if present
	if _, lerr := os.Lstat(linkAbs); lerr == nil {
		if err := os.RemoveAll(linkAbs); err != nil {
			return AgentSymlink{}, fmt.Errorf("remove existing path %s: %w", linkAbs, err)
		}
	}

	if err := os.Symlink(canonAbs, linkAbs); err == nil {
		return AgentSymlink{
			Agent:      agentID,
			LinkPath:   linkAbs,
			TargetPath: canonAbs,
			Mode:       "symlink",
		}, nil
	}

	// Fallback for environments where symlink creation is restricted (e.g. Windows without Developer Mode)
	if err := copyDirectoryRecursive(canonAbs, linkAbs); err != nil {
		return AgentSymlink{}, err
	}
	return AgentSymlink{
		Agent:      agentID,
		LinkPath:   linkAbs,
		TargetPath: canonAbs,
		Mode:       "copy",
	}, nil
}

func resolveCanonicalSkillDir(opts InstallOptions, homeDir, skillName string) (string, []string, error) {
	rawPath := strings.TrimSpace(opts.Path)
	rawTarget := strings.TrimSpace(opts.TargetDir)

	// Legacy --target <project-dir> support when --path is not set
	if rawPath == "" && rawTarget != "" {
		expanded, err := expandPath(rawTarget, homeDir)
		if err != nil {
			return "", nil, err
		}
		return filepath.Join(expanded, ".agents", "skills", skillName), []string{expanded}, nil
	}

	if rawPath == "" {
		rawPath = DefaultSkillsPath
	}
	expandedPath, err := expandPath(rawPath, homeDir)
	if err != nil {
		return "", nil, err
	}

	// If the provided path already ends with /<skillName>, use it directly; otherwise append <skillName>
	if filepath.Base(expandedPath) == skillName {
		return expandedPath, nil, nil
	}
	return filepath.Join(expandedPath, skillName), nil, nil
}

func expandPath(rawPath, homeDir string) (string, error) {
	p := strings.TrimSpace(rawPath)
	if p == "~" {
		return filepath.Abs(homeDir)
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(homeDir, p[2:])
	}
	return filepath.Abs(p)
}

func resolveAgentTargets(names []string) ([]AgentTarget, error) {
	if len(names) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	var out []AgentTarget

	addTarget := func(t AgentTarget) {
		if !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t)
		}
	}

	for _, raw := range names {
		for _, token := range strings.Split(raw, ",") {
			key := strings.ToLower(strings.TrimSpace(token))
			if key == "" {
				continue
			}
			if key == "all" || key == "*" {
				for _, t := range SupportedAgentTargets {
					addTarget(t)
				}
				continue
			}
			matched := false
			for _, t := range SupportedAgentTargets {
				if t.ID == key {
					addTarget(t)
					matched = true
					break
				}
				for _, alias := range t.Aliases {
					if alias == key {
						addTarget(t)
						matched = true
						break
					}
				}
				if matched {
					break
				}
			}
			if !matched {
				return nil, fmt.Errorf("unsupported agent target %q (supported: antigravity|agy, claude, codex, cursor, windsurf, copilot, opencode, cline, all)", token)
			}
		}
	}
	return out, nil
}

func formatSkillBinInvocation(skillDir, binDestPath, homeDir string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, relErr := filepath.Rel(cwd, binDestPath); relErr == nil && !strings.HasPrefix(rel, "..") {
			slashRel := filepath.ToSlash(rel)
			// When generating the repo template under ./skills/<skill-name>/scripts/fm,
			// emit the standard installed project path (.agents/skills/<skill-name>/scripts/fm)
			if strings.HasPrefix(slashRel, "skills/") {
				return ".agents/" + slashRel
			}
			return slashRel
		}
	}
	_ = skillDir
	_ = homeDir
	return filepath.ToSlash(binDestPath)
}

func copyDirectoryRecursive(srcDir, dstDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if info.Mode().Perm()&0o111 != 0 {
			return copyExecutable(path, target)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func copyExecutable(src, dst string) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if srcAbs == dstAbs {
		return os.Chmod(dstAbs, 0o755)
	}

	in, err := os.Open(srcAbs)
	if err != nil {
		return err
	}
	defer in.Close()

	tmpDst := dstAbs + ".tmp"
	out, err := os.OpenFile(tmpDst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmpDst)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if err := os.Chmod(tmpDst, 0o755); err != nil {
		_ = os.Remove(tmpDst)
		return err
	}
	return os.Rename(tmpDst, dstAbs)
}

func renderSkillMarkdown(skillName, binPath string) string {
	return fmt.Sprintf(`---
name: %s
description: >-
  Fetch simplified Figma design data (layout, Flexbox/CSS Grid, typography, rich text, colors, gradients, components)
  and download/crop SVG, PNG, and GIF assets using the standalone cross-platform fm CLI binary in scripts/.
  Use whenever the user shares a figma.com URL, fileKey, or asks to implement a UI from Figma.
---

# Figma Design & Asset Extraction Skill (`+"`fm`"+`)

This universal AI Agent Skill provides a zero-dependency, cross-platform CLI (`+"`%s`"+`) compatible with any AI Coding Agent (Antigravity, Claude Code, Codex, Cursor, Windsurf, Copilot, OpenCode, Cline, etc.) and powered by the **Figma-Context-MCP** single-pass extraction engine. It converts raw Figma REST API payloads into a token-optimized **Tree DSL** (`+"`tree`"+`), `+"`yaml`"+`, or `+"`json`"+` and downloads/crops SVG, PNG, and GIF assets.

## Authentication (Resolved on the Target Machine / Open Project)

When `+"`fm`"+` runs on the installed machine, it automatically resolves Figma credentials in this strict priority order:
1. **CLI flags**: `+"`--token <PAT>`"+` or `+"`--oauth-token <OAUTH>`"+`
2. **Open Project `+"`.env`"+`**: The `+"`.env`"+` file at the root of the project currently open in the AI Agent (e.g., `+"`./.env`"+` in the workspace root, even when `+"`fm`"+` is located inside `+"`./.agents/skills/figma-cli/scripts/fm`"+`)
3. **User Global `+"`~/.figma/.env`"+`**: The `+"`~/.figma/.env`"+` file in the user's home directory on the machine (can be set anytime via `+"`%s set-token <token>`"+`)
4. **Device / OS Environment**: `+"`FIGMA_API_KEY`"+` (Personal Access Token) or `+"`FIGMA_OAUTH_TOKEN`"+` configured in the operating system environment

---

## Standard 3-Step Workflow for Implementing Designs from Figma

### Step 1: Fetch Simplified Design Structure (`+"`fm fetch`"+`)

Run `+"`fm fetch`"+` with the Figma URL (or `+"`--file-key`"+` and `+"`--node-id`"+`). By default it outputs the compact **Tree DSL** (`+"`--format tree`"+`) which saves 40%%–55%% context tokens compared to JSON:

`+"```bash"+`
# Fetch directly from a Figma URL (automatically extracts fileKey and normalizes node-id)
%s fetch "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34"

# For large screens, save to a file and inspect sections selectively
%s fetch "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34" -o /tmp/figma-design.tree
`+"```"+`

For details on reading the `+"`tree`"+` DSL format (`+"`GLOBAL_VARS`"+`, `+"`ELEMENTS`"+` templates `+"`EL-xxxxxxxx`"+`, and inline rich-text `+"`{ts1}...{/ts1}`"+` tags), read [references/tree-dsl.md](./references/tree-dsl.md).

### Step 2: Download SVG Icons & Raster Images (`+"`fm download`"+`)

Inspect the `+"`NODES`"+` and `+"`GLOBAL_VARS`"+` output from Step 1:
- Nodes with `+"`[IMAGE-SVG]`"+` are vector icons/illustrations automatically collapsed by `+"`fm`"+` into a single downloadable SVG leaf.
- Fills with `+"`type: IMAGE`"+` (containing `+"`imageRef`"+`, `+"`gifRef`"+`, or `+"`imageDownloadArguments`"+` with `+"`cropTransform`"+` / `+"`filenameSuffix`"+`) are raster images.

By default, `+"`fm download`"+` saves files to the OS temporary directory (`+"`os.TempDir()`"+`). Pass `+"`--dir <path>`"+` to save directly into your project's asset directory (e.g., `+"`./public/images`"+` or `+"`./src/assets`"+`):

`+"```bash"+`
# Option A: Auto-discover and download ALL images & SVGs in a Figma URL/node to default OS tmp dir (or --dir)
%s download "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34" --dir ./public/images

# Option B: Download specific items using --item shorthand
%s download <fileKey> --dir ./public/images \
  --item "nodeId=10:22,file=ic-heart.svg" \
  --item "nodeId=10:25,imageRef=8f9a1b2c,file=hero.png"

# Option C: Download with full cropTransform / TILE dimension metadata via JSON manifest
%s download <fileKey> --dir ./public/images --manifest '[
  {
    "nodeId": "10:25",
    "imageRef": "8f9a1b2c",
    "fileName": "hero.png",
    "filenameSuffix": "3a9f1c",
    "needsCropping": true,
    "cropTransform": [[0.5, 0, 0.25], [0, 0.5, 0.1]]
  }
]'
`+"```"+`

### Step 3: Map Simplified Properties to Code

1. **Layout (`+"`mode: row | column | grid | none`"+`)**:
   - `+"`sizing.horizontal / vertical`"+`: `+"`fixed`"+` (use `+"`dimensions.width/height`"+`), `+"`fill`"+` (`+"`flex: 1`"+` / `+"`w-full`"+`), `+"`hug`"+` (`+"`w-fit`"+`), or `+"`contextual`"+` on the root node (use responsive container with `+"`designedWidth`"+` as a max-width hint).
2. **Colors & Gradients**:
   - Stacked solid fills are already alpha-blended into a single hex/rgba color.
   - Linear, radial, angular (`+"`conic-gradient`"+`), and diamond gradients are pre-converted to valid CSS syntax.
3. **TILE Mode Images**:
   - When `+"`scaleMode: TILE`"+` is present, copy the `+"`--original-width: ...; --original-height: ...;`"+` variables printed by `+"`fm download`"+` onto the element's inline style.
`, skillName, binPath, binPath, binPath, binPath, binPath, binPath, binPath)
}

func renderTreeDSLReference() string {
	return `# Tree DSL Format Reference

The default ` + "`tree`" + ` format of ` + "`fm fetch`" + ` encodes a ` + "`SimplifiedDesign`" + ` with minimal syntax overhead:

## 1. Document Sections

- **` + "`NAME`" + `**: The Figma file or frame title.
- **` + "`GLOBAL_VARS`" + `**: Shared styles referenced 2+ times across the tree, Named Figma Design System styles (e.g., ` + "`Primary / 600`" + `), and inline text-style overrides (` + "`ts1`" + `, ` + "`ts2`" + `).
- **` + "`ELEMENTS`" + `**: Deduplicated node body templates (` + "`EL-xxxxxxxx`" + `) for repeated structures (cards, list items, badges).
- **` + "`COMPONENTS`" + ` / ` + "`COMPONENT_SETS`" + `**: Component metadata and property definitions.
- **` + "`NODES`" + `**: Indented hierarchy (2 spaces per depth level), one node per line.

## 2. Node Line Syntax

` + "```text" + `
[TYPE] "Layer Name" #nodeId template=EL-xxxx layout=... fills=... strokes=... effects=... borderRadius=... text="..."
` + "```" + `

- **` + "`[TYPE]`" + `**: Node type in brackets (` + "`[FRAME]`" + `, ` + "`[TEXT]`" + `, ` + "`[INSTANCE]`" + `, ` + "`[IMAGE-SVG]`" + `).
- **` + `"Layer Name"` + `**: Omitted automatically for ` + "`TEXT`" + ` nodes and auto-generated Figma names (` + `"Frame 12"` + `, ` + `"Rectangle 5"` + `).
- **` + "`#nodeId`" + `**: Figma node ID (e.g., ` + "`#12:34`" + `). Pass ` + "`12:34`" + ` to ` + "`fm download`" + ` when downloading icons or node renders.
- **Single-use vs. Multi-use Styles**:
  - Styles used only once are **inlined** directly as compact JSON on the node line (` + `layout={"mode":"row","gap":"8px"}` + `).
  - Styles used 2+ times or Named Styles reference keys in ` + "`GLOBAL_VARS`" + ` (` + `fills=fill_a1b2c3d4` + `).
- **Rich Text (` + "`text=\"...\"`" + `)**:
  - Uses CommonMark (` + "`**bold**`" + `, ` + "`*italic*`" + `, ` + "`~~strike~~`" + `, ` + "`[text](url)`" + `) combined with ` + "`{ts1}...{/ts1}`" + ` tags referencing color/font overrides in ` + "`GLOBAL_VARS`" + `.
`
}
