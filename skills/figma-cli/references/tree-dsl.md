# Tree DSL Format Reference

The default `tree` format of `fm fetch` encodes a `SimplifiedDesign` with minimal syntax overhead:

## 1. Document Sections

- **`NAME`**: The Figma file or frame title.
- **`GLOBAL_VARS`**: Shared styles referenced 2+ times across the tree, Named Figma Design System styles (e.g., `Primary / 600`), and inline text-style overrides (`ts1`, `ts2`).
- **`ELEMENTS`**: Deduplicated node body templates (`EL-xxxxxxxx`) for repeated structures (cards, list items, badges).
- **`COMPONENTS` / `COMPONENT_SETS`**: Component metadata and property definitions.
- **`NODES`**: Indented hierarchy (2 spaces per depth level), one node per line.

## 2. Node Line Syntax

```text
[TYPE] "Layer Name" #nodeId template=EL-xxxx layout=... fills=... strokes=... effects=... borderRadius=... text="..."
```

- **`[TYPE]`**: Node type in brackets (`[FRAME]`, `[TEXT]`, `[INSTANCE]`, `[IMAGE-SVG]`).
- **"Layer Name"**: Omitted automatically for `TEXT` nodes and auto-generated Figma names ("Frame 12", "Rectangle 5").
- **`#nodeId`**: Figma node ID (e.g., `#12:34`). Pass `12:34` to `fm download` when downloading icons or node renders.
- **Single-use vs. Multi-use Styles**:
  - Styles used only once are **inlined** directly as compact JSON on the node line (layout={"mode":"row","gap":"8px"}).
  - Styles used 2+ times or Named Styles reference keys in `GLOBAL_VARS` (fills=fill_a1b2c3d4).
- **Rich Text (`text="..."`)**:
  - Uses CommonMark (`**bold**`, `*italic*`, `~~strike~~`, `[text](url)`) combined with `{ts1}...{/ts1}` tags referencing color/font overrides in `GLOBAL_VARS`.
