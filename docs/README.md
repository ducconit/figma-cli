# Tài liệu Kỹ thuật Toàn diện: `figma-cli` (`fm` v0.0.1) & Kiến trúc Gốc `Figma-Context-MCP`

Tài liệu này tổng hợp kết quả phân tích chuyên sâu toàn bộ thuật toán gốc của **[GLips/Figma-Context-MCP](https://github.com/GLips/Figma-Context-MCP)** và kiến trúc thực thi của **[github.com/ducconit/figma-cli](https://github.com/ducconit/figma-cli)** (**`fm` v0.0.1**) — công cụ Native Golang CLI & Universal Agent Skill (`skills/figma-cli`) tương thích với mọi AI Coding Agent (**Antigravity**, **Claude Code**, **Codex**, **Cursor**, **Windsurf**, **Copilot**, **OpenCode**, **Cline**).

---

## Kiến trúc Thực thi Native Golang của `figma-cli` (`fm` v0.0.1)

| Thành phần (`internal/*`) | Thiết kế & Tối ưu hóa trong Golang |
| :--- | :--- |
| **`internal/config`** | Phân giải thông tin xác thực trên **máy đã cài đặt** theo đúng 4 mức ưu tiên:<br>1. Cờ CLI (`--token`, `--oauth-token`)<br>2. File `<project>/.env` của dự án đang mở (tự động dò ngược lên thư mục gốc project kể cả khi gọi `fm` từ `<project>/.agents/skills/figma-cli/scripts/fm`)<br>3. File `~/.figma/.env` toàn cục của user (cập nhật nhanh bằng lệnh `fm set-token <token>`)<br>4. Biến môi trường của thiết bị/OS (`FIGMA_API_KEY`, `FIGMA_OAUTH_TOKEN`). |
| **`internal/figma`** | HTTP Client với Connection Pooling (`MaxIdleConnsPerHost: 20`), hỗ trợ Corporate Proxy, tự động che giấu token (`[REDACTED]`) trong mọi thông báo lỗi HTTP 403/429, tải file an toàn qua `.part` atomic rename, và chia batch gọi song song (`GetNodeRenderURLs`) khi số lượng node cần render lớn. |
| **`internal/extractors`** | Trích xuất đa nhánh song song (`walkNodes`) qua `sync.WaitGroup` + `nodeCh` channel và `sync/atomic` (`atomic.Int64` cho `tsCounter`), tính `StableStringify` & SHA-1 bên ngoài lock. Chạy song song `FinalizeDesign`, `simplifyComponents`, và `simplifyComponentSets` qua buffered channels (`finCh`, `compCh`, `compSetCh`). Khử trùng lặp `EL-xxxxxxxx` 1-pass song song qua `jobCh` & `entryCh`. |
| **`internal/images`** | **2-Stage Streaming Producer-Consumer Pipeline**: 3 producer goroutine gọi đồng thời `GetImageFillURLs`, `GetNodeRenderURLs("svg")`, `GetNodeRenderURLs("png")` và đẩy tác vụ vào `taskCh`; tập worker goroutine tải file + cắt ảnh Pure-Go theo ma trận affine $2 \times 3$ (`cropTransform`) và gửi kết quả qua `resultCh` (quản lý lỗi bằng `atomic.Pointer[error]`). |
| **`internal/skill`** | Cài đặt song song bộ skill chuẩn vào `~/.agents/skills/figma-cli` (hoặc `--path .agent/skills`) và tạo đồng loạt symlink cho các AI Agent (`--agy`, `--claude`, `--codex`, `--cursor`, `--windsurf`, `--copilot`, `--opencode`, `--cline`, `--all`) qua `linkCh`. |
| **Build & Release (`Makefile`)** | Biên dịch tĩnh `CGO_ENABLED=0` kèm bộ cờ giảm dung lượng binary (`-trimpath -buildvcs=false -gcflags="all=-l -B -C" -ldflags="-s -w -buildid="`) cho Linux, macOS, và Windows. |

---

## Danh mục Tài liệu Giải phẫu Thuật toán (`./docs`)

| STT | File tài liệu | Nội dung trọng tâm |
| :-: | :--- | :--- |
| **01** | [01-architecture-and-mcp-server.md](./01-architecture-and-mcp-server.md) | **Kiến trúc Tổng thể, Entry Points, Cấu hình & MCP Transports**<br>• Triết lý thiết kế Unix & phạm vi hệ thống<br>• 3 Entry points (`dist/bin.js`, `dist/index.js`, `dist/mcp-server.js`)<br>• Hệ thống cấu hình `resolve<T>` (CLI Flag → Env Var → Default)<br>• Chế độ `StdioServerTransport` vs Stateless `StreamableHTTPServerTransport` (`/mcp` & `/sse`)<br>• Per-request Authentication qua HTTP headers, Progress Heartbeat & Zod Validation Capture<br>• Chế độ CLI trực tiếp (`figma-developer-mcp fetch`) |
| **02** | [02-figma-api-and-connection.md](./02-figma-api-and-connection.md) | **Kết nối Figma REST API, Xác thực, Proxy & Xử lý Lỗi**<br>• `FigmaService`: Cơ chế xác thực kép PAT (`X-Figma-Token`) & OAuth (`Authorization: Bearer`)<br>• Chi tiết toàn bộ 5 endpoints Figma REST API v1 được sử dụng<br>• Hạ tầng HTTP (`fetchJSON`), đo dung lượng payload gốc và chống rò rỉ secret (`redactFromResponseBody`)<br>• Hỗ trợ Corporate Proxy với `undici` (`ProxyAgent`, `EnvHttpProxyAgent`, `--proxy=none`)<br>• Phân tích lỗi chuyên sâu: HTTP 429 Rate Limit (kèm seat/plan headers), HTTP 403 Forbidden & `ErrorMeta` symbol tagging<br>• Bộ phân tích URL Figma (`parseFigmaUrl`) và chuẩn hóa `nodeId` |
| **03** | [03-data-extraction-and-tree-walking.md](./03-data-extraction-and-tree-walking.md) | **Hệ thống Extractor, Duyệt Cây 1 Lượt & Tối ưu Hóa Toàn Cục**<br>• Kiến trúc 3 tầng: Strategy Layer, Traversal Layer, Extraction Layer<br>• Luồng `simplifyRawFigmaObject` và tách metadata cấp File/Node<br>• Thuật toán duyệt cây `extractFromDesign` (non-blocking `maybeYield`, lọc `isVisible`, giải cứu node ẩn trong Component Definition, sắp xếp thứ tự con trong Grid)<br>• Tự động gộp nhóm Vector thành `IMAGE-SVG` (`collapseSvgContainers`)<br>• Quản lý biến style định danh theo nội dung (`findOrCreateVar` SHA-1, `resolveStyleKey` cho Named Styles)<br>• Hậu xử lý `finalizeDesign`: Count-gated style inlining, deduplicate `elements` (`EL-xxxxxxxx`), và khử Double Indirection |
| **04** | [04-transformers-and-css-mapping.md](./04-transformers-and-css-mapping.md) | **Hệ thống Transformers: Chuyển đổi Figma sang CSS / Web Semantics**<br>• **Layout Transformer**: Auto Layout (`HORIZONTAL`/`VERTICAL` → Flexbox, `GRID` → CSS Grid, packed vs sparse grid, root `contextual` sizing, `locationRelativeToParent`)<br>• **Style Transformer**: Màu RGBA/Hex, làm phẳng chồng màu đặc (`flattenSolidFills` bằng Source-Over Alpha Compositing)<br>• **Gradient Math**: Phép chiếu đường thẳng mở rộng và công thức góc cho `linear-gradient`, `radial-gradient`, `conic-gradient`<br>• **Image & Pattern Fills**: Ánh xạ `scaleMode` (`FILL`, `FIT`, `TILE`, `STRETCH`) và ma trận affine `imageTransform`<br>• **Rich Text Engine**: Kết hợp Markdown (`**`, `*`, `~~`, `[](url)`) và thẻ inline style `{tsN}...{/tsN}` từ `characterStyleOverrides`<br>• **Effects & Components**: `boxShadow`/`textShadow`, chia đôi bán kính `blur(r/2)`, rút gọn Component Properties & References |
| **05** | [05-image-pipeline-and-post-processing.md](./05-image-pipeline-and-post-processing.md) | **Pipeline Tải Ảnh, Khử Trùng Lặp, Cắt Ảnh Jimp & Bảo mật Đường dẫn**<br>• Phân biệt 3 nguồn ảnh: Static Fill (`imageRef`), Animated GIF (`gifRef`), và Rendered Node (`nodeId` PNG/SVG)<br>• Thuật toán Deduplication lượt tải & gắn hậu tố băm ma trận (`filenameSuffix`)<br>• Xử lý hậu kỳ với `@jimp/core`: Công thức toán học cắt ảnh (`applyCropTransform`) từ ma trận affine $2 \times 3$<br>• Đọc kích thước SVG (`parseSvgDimensions`) và sinh biến CSS `--original-width` / `--original-height` cho chế độ `TILE`<br>• 3 lớp bảo vệ chống tấn công Path Traversal (`resolveLocalPath`, `isWithin`, Zod regex) |
| **06** | [06-serialization-telemetry-and-capabilities.md](./06-serialization-telemetry-and-capabilities.md) | **Serialization (Tree DSL / YAML / JSON), Telemetry & Tổng hợp Khả năng**<br>• Làm sạch tên node nhiễu (`stripNoiseName`: loại bỏ tên tự sinh `Frame 12` và tên thừa trên node `TEXT`)<br>• Giải phẫu định dạng `tree` (Compact DSL mặc định) giúp tiết kiệm **40%–55% token** so với JSON<br>• Cấu hình `yaml` (`JSON_SCHEMA`, `noRefs`) và `json`<br>• Kiến trúc Telemetry ẩn danh (PostHog) & cơ chế Redaction bảo mật 2 tầng qua `AsyncLocalStorage`<br>• Các công cụ Benchmark, CPU Profiling & Quét ký tự ẩn chống Prompt Injection (`scripts/*`)<br>• Bảng tổng hợp toàn bộ khả năng (Capabilities Matrix) của `Figma-Context-MCP` |

---

## Sơ đồ Kiến trúc Luồng Dữ liệu Tổng thể (End-to-End Pipeline)

```mermaid
flowchart TD
    subgraph ClientLayer["1. AI Coding Agent / CLI Caller"]
        MCPClient["MCP Client (Cursor, Claude, Windsurf...)"]
        CLIUser["CLI User (`figma-developer-mcp fetch`)"]
    end

    subgraph EntryAndTransport["2. Entry Points & Transports (`src/bin.ts`, `src/server.ts`, `src/mcp/`)"]
        Stdio["StdioServerTransport (`--stdio`)"]
        HTTP["StreamableHTTP (`/mcp`, `/sse`)\nPer-request Header Auth"]
        FetchCmd["CLI `fetch` Command (`src/commands/fetch.ts`)"]
        Tools["MCP Tools:\n• `get_figma_data`\n• `download_figma_images`"]
    end

    subgraph ServiceAndNetwork["3. Figma API & Network Layer (`src/services/`, `src/utils/fetch-json.ts`)"]
        FigmaSvc["`FigmaService`\n• PAT (`X-Figma-Token`)\n• OAuth (`Bearer`)\n• Proxy (`EnvHttpProxyAgent`)"]
        FigmaAPI[("Figma REST API v1\n• `/v1/files/:key`\n• `/v1/files/:key/nodes`\n• `/v1/files/:key/images`\n• `/v1/images/:key`")]
    end

    subgraph ExtractionPipeline["4. Single-Pass Extraction & Finalization (`src/extractors/`, `src/transformers/`)"]
        Parser["`parseAPIResponse`\nFile/Node Metadata + Styles"]
        Walker["`extractFromDesign` (Single-Pass Tree Walk)\n• `layoutExtractor` (Flex / Grid / Absolute)\n• `textExtractor` (Markdown + `{tsN}` Rich Text)\n• `visualsExtractor` (Alpha-blend Fills / Gradients / Strokes / Effects)\n• `componentExtractor` (Props & Definitions)\n• `collapseSvgContainers` (Vector Group → `IMAGE-SVG`)"]
        Finalizer["`finalizeDesign` (Global Optimization)\n1. `inlineSingleUseStyles` (Count-gated hoisting)\n2. `deduplicateElements` (`EL-xxxxxxxx` templates)\n3. `inlineExclusiveStyles` (Remove double indirection)"]
    end

    subgraph OutputAndPostProc["5. Serialization & Image Post-Processing (`src/utils/`)"]
        Serializer["`wrapForSerialization` (`stripNoiseName`)\n→ `serializeResult`:\n• `tree` (Compact DSL - Default MCP)\n• `yaml` (Default CLI)\n• `json`"]
        ImgPipeline["`downloadFigmaImages`\n• Deduplicate `imageRef`\n• Jimp 2x3 Matrix Crop (`applyCropTransform`)\n• SVG Dimension Parser\n• CSS `--original-width/height` for `TILE`\n• Lexical Sandbox (`resolveLocalPath`)"]
    end

    MCPClient --> Stdio & HTTP --> Tools
    CLIUser --> FetchCmd
    Tools & FetchCmd --> FigmaSvc
    FigmaSvc <--> FigmaAPI
    FigmaSvc --> Parser --> Walker --> Finalizer --> Serializer --> MCPClient & CLIUser
    FigmaSvc --> ImgPipeline --> MCPClient
```

---

## Tóm tắt Nhanh các Đổi mới Kỹ thuật Cốt lõi của Repo

1. **Tối ưu hóa Token triệt để cho LLM (Context-Window Engineering)**:
   - Lược bỏ mọi giá trị mặc định mà LLM tự suy luận được (`justifyContent: flex-start`, `strokeAlign: INSIDE`, `italic: false`, `lineHeightUnit: INTRINSIC_%`, `opacity: 1`).
   - Làm phẳng nhiều lớp màu `SOLID` chồng nhau thành 1 mã màu duy nhất bằng công thức **Source-Over Alpha Compositing** để LLM không chọn nhầm layer màu bị che.
   - Tự động phát hiện các `FRAME`/`GROUP`/`INSTANCE` chỉ chứa các hình học vector và thu gọn toàn bộ nhánh con thành một node lá duy nhất có `type: "IMAGE-SVG"`, ngăn chặn hàng nghìn token rác từ các thẻ path SVG phức tạp.
   - Cơ chế **Count-Gated Style Hoisting**: Chỉ giữ biến trong `globalVars.styles` nếu được dùng từ 2 lần trở lên hoặc là Named Style của Design System; nếu chỉ dùng 1 lần thì inline ngược vào node để xóa bỏ chi phí tra cứu gián tiếp (indirection tax).
   - Khử trùng lặp cả thân phần tử (`ElementBody`) thành các mẫu `EL-xxxxxxxx` trong bảng `elements`, đồng thời tự động inline các style độc quyền của mẫu đó.
   - Định dạng đầu ra mặc định `tree` mã hóa mỗi node trên đúng 1 dòng (`[TYPE] "name" #id key=val ...`), giảm 40%–55% dung lượng so với JSON.

2. **Chuyển đổi Chính xác Ngữ nghĩa Thiết kế sang CSS**:
   - Hỗ trợ đầy đủ cả **Flexbox** (`row`/`column`, `wrap`, tự động phát hiện `alignItems: stretch` khi toàn bộ con `FILL`) và **CSS Grid** (sắp xếp lại thứ tự DOM theo `gridRowAnchorIndex`/`gridColumnAnchorIndex`, phân biệt lưới đặc `isPackedGrid` và lưới thưa, tự động gán `zIndex` khi các ô giao nhau).
   - Giải bài toán **Linear Gradient** của Figma bằng hình học giải tích: tìm giao điểm của đoạn thẳng handle với hình vuông đơn vị $[0, 1] \times [0, 1]$ để quy đổi vị trí phần trăm của các `gradientStops` sang hệ quy chiếu CSS.
   - Giải bài toán **Rich Text** bằng bộ phân đoạn ký tự (`characterStyleOverrides`), kết hợp cú pháp Markdown chuẩn CommonMark với các thẻ tham chiếu style cục bộ `{ts1}...{/ts1}`.
   - Giải bài toán **Image Crop & Tile**: dùng `@jimp/core` cắt ảnh trực tiếp trên đĩa theo ma trận affine $2 \times 3$ (`imageTransform`) của Figma và tính toán biến CSS `--original-width` / `--original-height` cho chế độ lặp `TILE`.
