package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/ducconit/figma-cli/internal/config"
	"github.com/ducconit/figma-cli/internal/skill"
	"github.com/spf13/cobra"
)

func newInstallCmd(global *GlobalFlags) *cobra.Command {
	var (
		skillsPath  string
		targetDir   string
		skillName   string
		globalMode  bool
		targetOS    string
		homeDir     string
		agentList   []string
		flagAntig   bool
		flagAgy     bool
		flagClaude  bool
		flagCodex   bool
		flagCursor  bool
		flagWind    bool
		flagCopilot bool
		flagOpen    bool
		flagCline   bool
		flagAll     bool
		flagMultiOS bool
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the universal Figma Agent Skill (with scripts/fm binary), agent symlinks, and access token",
		Long: `Install the Figma Agent Skill for any AI Coding Agent across Linux, macOS, and Windows.
By default, installs into ~/.agents/skills/<skill-name>/ (` + skill.DefaultSkillsPath + `) and saves credentials to ~/.figma/.env:
  <path>/<skill-name>/
    ├── SKILL.md
    ├── references/
    │   └── tree-dsl.md
    └── scripts/
        └── fm (or fm.exe on Windows)

Use --path to specify a custom skills directory (e.g. --path .agent/skills for a project-local skill).
Use quick agent flags (--antigravity|--agy, --claude, --codex, --cursor, --windsurf, --copilot, --opencode, --cline, --all)
to automatically create symlinks from each agent's global skills folder to the installed skill:
  • --antigravity / --agy -> ~/.gemini/config/skills/<skill-name>
  • --claude              -> ~/.claude/skills/<skill-name>
  • --codex               -> ~/.codex/skills/<skill-name>
  • --cursor              -> ~/.cursor/skills/<skill-name>
  • --windsurf            -> ~/.codeium/windsurf/skills/<skill-name>
  • --copilot             -> ~/.copilot/skills/<skill-name>
  • --opencode            -> ~/.config/opencode/skills/<skill-name>
  • --cline               -> ~/.cline/skills/<skill-name>

Examples:
  # Install into default ~/.agents/skills/figma-cli and symlink to Antigravity, Claude Code, and Codex
  fm install --agy --claude --codex --token figd_YOUR_PERSONAL_ACCESS_TOKEN

  # Install into a project-local skill folder (.agent/skills/figma-cli)
  fm install --path .agent/skills --token figd_YOUR_PERSONAL_ACCESS_TOKEN

  # Install into ~/.agents/skills/figma-cli and symlink to ALL supported AI agents
  fm install --all --token figd_YOUR_TOKEN`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tok := strings.TrimSpace(global.Token)
			if tok == "" {
				tok = strings.TrimSpace(os.Getenv("FIGMA_API_KEY"))
			}
			oauthTok := strings.TrimSpace(global.OAuthToken)
			if oauthTok == "" {
				oauthTok = strings.TrimSpace(os.Getenv("FIGMA_OAUTH_TOKEN"))
			}

			var agents []string
			agents = append(agents, agentList...)
			if flagAntig || flagAgy {
				agents = append(agents, "antigravity")
			}
			if flagClaude {
				agents = append(agents, "claude")
			}
			if flagCodex {
				agents = append(agents, "codex")
			}
			if flagCursor {
				agents = append(agents, "cursor")
			}
			if flagWind {
				agents = append(agents, "windsurf")
			}
			if flagCopilot {
				agents = append(agents, "copilot")
			}
			if flagOpen {
				agents = append(agents, "opencode")
			}
			if flagCline {
				agents = append(agents, "cline")
			}
			if flagAll {
				agents = append(agents, "all")
			}

			var multiOSOpt *bool
			if cmd.Flags().Changed("multi-os") {
				multiOSOpt = &flagMultiOS
			}

			report, err := skill.Install(skill.InstallOptions{
				Path:       skillsPath,
				TargetDir:  targetDir,
				HomeDir:    homeDir,
				SkillName:  skillName,
				Token:      tok,
				OAuthToken: oauthTok,
				TargetOS:   targetOS,
				Global:     globalMode,
				Agents:     agents,
				MultiOS:    multiOSOpt,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Installed Figma Agent Skill for %s/%s:\n", report.OS, report.Arch)
			fmt.Fprintf(out, "  - Skill directory : %s\n", report.SkillDir)
			fmt.Fprintf(out, "  - Skill manifest  : %s\n", report.SkillMDPath)
			fmt.Fprintf(out, "  - DSL reference   : %s\n", report.ReferenceMDPath)
			fmt.Fprintf(out, "  - Binary in script: %s\n", report.BinaryDestPath)
			if len(report.Symlinks) > 0 {
				fmt.Fprintf(out, "  - Agent symlinks  :\n")
				for _, link := range report.Symlinks {
					fmt.Fprintf(out, "      • %-11s : %s -> %s (%s)\n", link.Agent, link.LinkPath, link.TargetPath, link.Mode)
				}
			}
			if report.TokenConfigured {
				masked := config.MaskToken(tok)
				if oauthTok != "" {
					masked = config.MaskToken(oauthTok)
				}
				fmt.Fprintf(out, "  - Access token    : %s (saved to %s)\n", masked, report.EnvFilePath)
			} else {
				fmt.Fprintf(out, "  - Access token    : not specified (set FIGMA_API_KEY in environment or re-run with --token)\n")
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&skillsPath, "path", "p", "", "Base directory to install skill (default: ~/.agents/skills; project install: .agent/skills)")
	cmd.Flags().StringVar(&skillName, "skill-name", skill.DefaultSkillName, "Name of the installed skill folder")
	cmd.Flags().BoolVar(&flagAntig, "antigravity", false, "Create symlink in Google Antigravity skills directory (~/.gemini/config/skills)")
	cmd.Flags().BoolVar(&flagAgy, "agy", false, "Shorthand for --antigravity (~/.gemini/config/skills)")
	cmd.Flags().BoolVar(&flagClaude, "claude", false, "Create symlink in Claude Code skills directory (~/.claude/skills)")
	cmd.Flags().BoolVar(&flagCodex, "codex", false, "Create symlink in OpenAI Codex skills directory (~/.codex/skills)")
	cmd.Flags().BoolVar(&flagCursor, "cursor", false, "Create symlink in Cursor skills directory (~/.cursor/skills)")
	cmd.Flags().BoolVar(&flagWind, "windsurf", false, "Create symlink in Windsurf skills directory (~/.codeium/windsurf/skills)")
	cmd.Flags().BoolVar(&flagCopilot, "copilot", false, "Create symlink in GitHub Copilot skills directory (~/.copilot/skills)")
	cmd.Flags().BoolVar(&flagOpen, "opencode", false, "Create symlink in OpenCode skills directory (~/.config/opencode/skills)")
	cmd.Flags().BoolVar(&flagCline, "cline", false, "Create symlink in Cline skills directory (~/.cline/skills)")
	cmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Create symlinks for all supported AI agents")
	cmd.Flags().BoolVar(&flagMultiOS, "multi-os", false, "Generate multi-OS documentation in SKILL.md (used when bundling scripts for all platforms, e.g. for npx skills add)")
	cmd.Flags().StringSliceVar(&agentList, "agent", nil, "Agent name(s) to symlink (antigravity|agy, claude, codex, cursor, windsurf, copilot, opencode, cline, all)")

	// Backward-compatible & internal test flags
	cmd.Flags().StringVarP(&targetDir, "target", "t", "", "Project root directory to install <target>/.agents/skills/<skill-name>")
	cmd.Flags().BoolVarP(&globalMode, "global", "g", false, "Alias for --antigravity (symlink to ~/.gemini/config/skills)")
	cmd.Flags().StringVar(&targetOS, "os", "", "Override target OS name (linux, darwin, windows)")
	cmd.Flags().StringVar(&homeDir, "home-dir", "", "Override user home directory")
	_ = cmd.Flags().MarkHidden("os")
	_ = cmd.Flags().MarkHidden("home-dir")

	return cmd
}
