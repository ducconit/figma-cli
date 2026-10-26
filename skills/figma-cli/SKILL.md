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

## Standard 3-Step Workflow for Implementing Designs from Figma

### Step 1: Fetch Simplified Design Structure (`fm fetch`)

Run `fm fetch` with the Figma URL (or `--file-key` and `--node-id`). By default it outputs the compact **Tree DSL** (`--format tree`) which saves 40%–55% context tokens compared to JSON:

```bash
# Fetch directly from a Figma URL (automatically extracts fileKey and normalizes node-id)
.agents/skills/figma-cli/scripts/fm fetch "https://www.figma.com/design/<fileKey>/<fileName>?node-id=12-34"

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
