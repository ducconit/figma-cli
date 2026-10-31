---
name: figma-cli
description: >-
  Fetch simplified Figma design data (layout, Flexbox/CSS Grid, typography, rich text, colors, gradients, components)
  and download/crop SVG, PNG, and GIF assets using the standalone cross-platform fm CLI binary in scripts/.
  Use whenever the user shares a figma.com URL, fileKey, or asks to implement a UI from Figma.
---

# Figma Design & Asset Extraction Skill (`fm`)

This universal AI Agent Skill provides a zero-dependency, cross-platform CLI (`.agents/skills/figma-cli/scripts/fm`) compatible with any AI Coding Agent (Antigravity, Claude Code, Codex, Cursor, Windsurf, Copilot, OpenCode, Cline, etc.) and powered by the **Figma-Context-MCP** single-pass extraction engine. It converts raw Figma REST API payloads into a token-optimized **Tree DSL** (`tree`), `yaml`, or `json` and downloads/crops SVG, PNG, and GIF assets.

## Authentication (Resolved on the Target Machine / Open Project)

When `fm` runs on the installed machine, it automatically resolves Figma credentials in this strict priority order:
1. **CLI flags**: `--token <PAT>` or `--oauth-token <OAUTH>`
2. **Open Project `.env`**: The `.env` file at the root of the project currently open in the AI Agent (e.g., `./.env` in the workspace root, even when `fm` is located inside `./.agents/skills/figma-cli/scripts/fm`)
3. **User Global `~/.figma/.env`**: The `~/.figma/.env` file in the user's home directory on the machine (can be set anytime via `.agents/skills/figma-cli/scripts/fm set-token <token>`)
4. **Device / OS Environment**: `FIGMA_API_KEY` (Personal Access Token) or `FIGMA_OAUTH_TOKEN` configured in the operating system environment

---

## Operating System & Binary Selection

The `scripts/` directory provides native pre-compiled binaries for each operating system:

| Operating System | Architecture | Binary Path |
| :--- | :--- | :--- |
| **Linux** | x86_64 / amd64 | `.agents/skills/figma-cli/scripts/fm` (or `fm-linux-amd64`) |
| **macOS** | Apple Silicon (M1/M2/M3/M4, `arm64`) | `.agents/skills/figma-cli/scripts/fm-darwin-arm64` |
| **Windows** | x86_64 / 64-bit | `.agents/skills/figma-cli/scripts/fm.exe` (or `fm-windows-amd64.exe`) |

> [!IMPORTANT]
> **AI Agent Execution Rule**:
> - Detect your current OS before running commands:
>   - **macOS**: Run `.agents/skills/figma-cli/scripts/fm-darwin-arm64`.
>   - **Linux**: Run `.agents/skills/figma-cli/scripts/fm`.
>   - **Windows**: Run `.agents/skills/figma-cli/scripts/fm.exe`.
> - If running inside the repo or custom path, adjust the prefix accordingly (e.g. `skills/figma-cli/scripts/...`).

---

## Standard 3-Step Workflow for Implementing Designs from Figma

### Step 1: Fetch Simplified Design Structure (`fm fetch`)

Run `fm fetch` using your OS-appropriate binary with the Figma URL (or `--file-key` and `--node-id`). By default it outputs the compact **Tree DSL** (`--format tree`) which saves 40%–55% context tokens compared to JSON:

```bash
# macOS (Apple Silicon):
.agents/skills/figma-cli/scripts/fm-darwin-arm64 fetch "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34"

# Linux:
.agents/skills/figma-cli/scripts/fm fetch "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34"

# Windows (PowerShell):
.agents/skills/figma-cli/scripts/fm.exe fetch "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34"

# For large screens, save to a file and inspect sections selectively
.agents/skills/figma-cli/scripts/fm fetch "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34" -o /tmp/figma-design.tree
```

For details on reading the `tree` DSL format (`GLOBAL_VARS`, `ELEMENTS` templates `EL-xxxxxxxx`, and inline rich-text `{ts1}...{/ts1}` tags), read [references/tree-dsl.md](./references/tree-dsl.md).

### Step 2: Download SVG Icons & Raster Images (`fm download`)

Inspect the `NODES` and `GLOBAL_VARS` output from Step 1:
- Nodes with `[IMAGE-SVG]` are vector icons/illustrations automatically collapsed by `fm` into a single downloadable SVG leaf.
- Fills with `type: IMAGE` (containing `imageRef`, `gifRef`, or `imageDownloadArguments` with `cropTransform` / `filenameSuffix`) are raster images.

By default, `fm download` saves files to the OS temporary directory (`os.TempDir()`). Pass `--dir <path>` to save directly into your project's asset directory (e.g., `./public/images` or `./src/assets`):

```bash
# Option A: Auto-discover and download ALL images & SVGs in a Figma URL/node to default OS tmp dir (or --dir)
# Replace with your OS binary (e.g. .agents/skills/figma-cli/scripts/fm-darwin-arm64 on Apple Silicon Mac, .agents/skills/figma-cli/scripts/fm on Linux, .agents/skills/figma-cli/scripts/fm.exe on Windows)
.agents/skills/figma-cli/scripts/fm download "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34" --dir ./public/images

# Option B: Download specific items using --item shorthand
.agents/skills/figma-cli/scripts/fm download <fileKey> --dir ./public/images \
  --item "nodeId=10:22,file=ic-heart.svg" \
  --item "nodeId=10:25,imageRef=8f9a1b2c,file=hero.png"

# Option C: Download with full cropTransform / TILE dimension metadata via JSON manifest
.agents/skills/figma-cli/scripts/fm download <fileKey> --dir ./public/images --manifest '[
  {
    "nodeId": "10:25",
    "imageRef": "8f9a1b2c",
    "fileName": "hero.png",
    "filenameSuffix": "3a9f1c",
    "needsCropping": true,
    "cropTransform": [[0.5, 0, 0.25], [0, 0.5, 0.1]]
  }
]'
```

### Step 3: Map Simplified Properties to Code

1. **Layout (`mode: row | column | grid | none`)**:
   - `sizing.horizontal / vertical`: `fixed` (use `dimensions.width/height`), `fill` (`flex: 1` / `w-full`), `hug` (`w-fit`), or `contextual` on the root node (use responsive container with `designedWidth` as a max-width hint).
2. **Colors & Gradients**:
   - Stacked solid fills are already alpha-blended into a single hex/rgba color.
   - Linear, radial, angular (`conic-gradient`), and diamond gradients are pre-converted to valid CSS syntax.
3. **TILE Mode Images**:
   - When `scaleMode: TILE` is present, copy the `--original-width: ...; --original-height: ...;` variables printed by `fm download` onto the element's inline style.

---

## Additional Capabilities

### Extract Design Tokens (`fm tokens`)

Extract Figma Variables (colors, spacing, radii, typography, dark/light modes) into CSS Variables, Tailwind v3/v4 (`@theme`), SwiftUI (`DesignTokens`), DTCG (W3C standard), or JSON:

```bash
# Export CSS variables (:root / .dark)
.agents/skills/figma-cli/scripts/fm tokens "https://www.figma.com/design/<fileKey>/<fileName>" --format css -o ./src/styles/tokens.css

# Export Tailwind v4 @theme CSS
.agents/skills/figma-cli/scripts/fm tokens <fileKey> --format tailwind-v4 -o ./src/styles/theme.css

# Export Tailwind v3 config extension
.agents/skills/figma-cli/scripts/fm tokens <fileKey> --format tailwind -o ./tailwind.tokens.js

# Export native SwiftUI enum
.agents/skills/figma-cli/scripts/fm tokens <fileKey> --format swiftui -o ./DesignTokens.swift

# Export W3C Design Token Community Group (DTCG) JSON
.agents/skills/figma-cli/scripts/fm tokens <fileKey> --format dtcg -o ./tokens.json
```

### Export Design System to Google Stitch DESIGN.md (`fm stitch`)

Extract and generate a 100% compliant [Google Stitch DESIGN.md](https://stitch.withgoogle.com/docs/design-md/specification/) specification from any Figma URL, File Key, or Design System Canvas Page:

```bash
# Export DESIGN.md from a Figma Design System canvas page or URL
.agents/skills/figma-cli/scripts/fm stitch "https://www.figma.com/design/<fileKey>/<fileName>?node-id=1:133" -o DESIGN.md

# Force Canvas heuristic extraction for Community UI Kits / Files without Variables
.agents/skills/figma-cli/scripts/fm stitch <fileKey> --force-canvas -o DESIGN.md

# Validate with official Google Stitch linter
npx -y @google/design.md lint DESIGN.md

# Export generated tokens directly to Tailwind v4 @theme, DTCG, or CSS Variables
npx -y @google/design.md export DESIGN.md --format css-tailwind
npx -y @google/design.md export DESIGN.md --format dtcg
```

### Visual Screenshots (`fm screenshot`)

Render and download pixel-accurate PNG, JPG, or SVG visual snapshots of Figma frames or components for automated visual verification:

```bash
# Render a 2x PNG screenshot of a frame
.agents/skills/figma-cli/scripts/fm screenshot "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34" --scale 2 -o ./tmp/screenshot.png

# Output screenshot URL and file metadata as JSON
.agents/skills/figma-cli/scripts/fm screenshot <fileKey> --node-id 12:34 --json
```

### Motion & Prototype Animations (`fm motion`)

Extract prototype interactions, smart animate transitions, durations, and cubic-bezier easing curves into CSS `@keyframes`, Framer Motion / `motion.dev`, or JSON:

```bash
# Export CSS @keyframes and transition utility classes
.agents/skills/figma-cli/scripts/fm motion "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34" --format css -o ./src/styles/motion.css

# Export Framer Motion / motion.dev transition configs
.agents/skills/figma-cli/scripts/fm motion <fileKey> --node-id 12:34 --format framer -o ./src/motion.ts
```

### Code Connect (`fm code-connect`)

Scan local frontend components (`.tsx`, `.jsx`, `.vue`, `.swift`), fuzzy-match them against Figma components/component sets, generate parserless `.figma.ts` templates, and attach Dev Resources:

```bash
# Scan local directory and report match percentages against Figma
.agents/skills/figma-cli/scripts/fm code-connect scan "https://www.figma.com/design/<fileKey>/<fileName>" --dir ./src

# Generate .figma.ts Code Connect templates for matched components
.agents/skills/figma-cli/scripts/fm code-connect generate <fileKey> --dir ./src --out-dir ./figma

# Attach repository source URLs to Figma component nodes via Dev Resources API
.agents/skills/figma-cli/scripts/fm code-connect link <fileKey> --dir ./src --repo-url "https://github.com/my-org/my-repo/blob/main"
```

### Model Context Protocol (MCP) Server (`fm mcp`)

Start a zero-dependency JSON-RPC 2.0 MCP server over stdio for AI Coding Agents (Antigravity, Cursor, Claude Code, Windsurf):

```json
{
  "mcpServers": {
    "figma-cli": {
      "command": "/path/to/fm",
      "args": ["mcp"]
    }
  }
}
```

Exposed MCP tools:
- `get_design_context`: Fetch and simplify Figma Design trees into token-efficient Tree DSL, YAML, or JSON.
- `get_screenshot`: Render high-DPI screenshots of frames/nodes for visual verification.
- `get_tokens`: Extract Figma Variables into CSS, Tailwind v3/v4, SwiftUI, DTCG, or JSON.
- `get_motion_context`: Extract prototype animations, transitions, and easing curves.
- `download_assets`: Auto-discover, download, and crop SVG/PNG/GIF assets.
- `code_connect`: Match codebase components with Figma components and generate `.figma.ts`.

### Manage Dev Mode Resources (`fm link`)

Attach or view code implementation links (GitHub PR, Storybook, Jira) directly on Figma nodes:

```bash
# Attach a link to a node
.agents/skills/figma-cli/scripts/fm link "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34" --url "https://github.com/org/repo/pull/42" --name "PR #42"

# List links on a node or file
.agents/skills/figma-cli/scripts/fm link "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34" --list
```
