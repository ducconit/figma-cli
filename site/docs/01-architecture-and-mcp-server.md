# 01. Kiến trúc Tổng thể, Entry Points, Cấu hình & MCP Server (`Figma-Context-MCP`)

Tài liệu này phân tích chi tiết kiến trúc phân tầng, các điểm khởi chạy (entry points), cơ chế cấu hình, hệ thống giao thức MCP (Stdio & StreamableHTTP), xác thực theo từng request, cơ chế thông báo tiến độ (progress heartbeat), kiểm tra dữ liệu đầu vào (Zod validation capture) và công cụ CLI trực tiếp của `figma-developer-mcp@0.13.2`.

---

## 1. Triết lý Thiết kế & Kiến trúc Phân tầng

### 1.1. Triết lý Thiết kế (`CONTRIBUTING.md`, `CLAUDE.md`)
1. **Unix Philosophy cho MCP Tools**:
   - Mỗi công cụ (tool) chỉ thực hiện **một nhiệm vụ duy nhất** với số lượng tham số tối thiểu để tránh gây nhiễu cho LLM khi quyết định gọi hàm.
   - Các tùy chọn mang tính toàn cục cấp dự án (như định dạng đầu ra `tree`/`yaml`/`json`, thư mục lưu ảnh `imageDir`, bật/tắt tải ảnh `skipImageDownloads`) được cấu hình qua **CLI flags hoặc biến môi trường** lúc khởi chạy server thay vì đưa vào schema tham số của từng lần gọi tool.
2. **Phạm vi Tập trung (Focused Scope)**:
   - Hệ thống chỉ giải quyết bài toán **"Ingesting designs for AI consumption"** (thu thập, làm sạch, rút gọn và chuẩn hóa dữ liệu thiết kế Figma cho AI).
   - Không ôm đồm việc sinh code framework cụ thể (React/Vue/Tailwind), đồng bộ CMS hay chỉnh sửa đồ họa ngoài việc tải và cắt ảnh (crop) đúng theo ma trận biến đổi của Figma.
3. **Functional Core, Imperative Shell**:
   - Toàn bộ logic biến đổi dữ liệu (`src/extractors/`, `src/transformers/`, `src/utils/serialize*.ts`) là các hàm thuần túy (pure functions) không phụ thuộc vào trạng thái mạng hay giao thức MCP, giúp dễ dàng tái sử dụng dưới dạng thư viện độc lập hoặc chạy qua CLI.

---

### 1.2. Ba Entry Points Độc lập (`tsup.config.ts`, `package.json`)

Theo cấu hình đóng gói trong `/tmp/Figma-Context-MCP/tsup.config.ts`, dự án biên dịch ra 3 điểm nhập ESM (`dist/*.js`) phục vụ 3 mục đích khác nhau:

| Entry Point (Source → Dist) | Khai báo `package.json` | Vai trò & Các thành phần Export chính |
| :--- | :--- | :--- |
| **`src/bin.ts` → `dist/bin.js`** | `"bin": { "figma-developer-mcp": "dist/bin.js" }` | Binary thực thi chính. Dùng thư viện `cleye` để phân tích tham số dòng lệnh và điều hướng giữa lệnh con `fetch` (`src/commands/fetch.ts`) hoặc khởi chạy MCP Server (`src/server.ts`). |
| **`src/index.ts` → `dist/index.js`** | `"main": "dist/index.js"` | **Library Entry (Pure Extractors)**: Chỉ export hệ thống trích xuất dữ liệu và kiểu TypeScript mà không kéo theo Express hay MCP SDK:<br>• Types: `SimplifiedDesign`, `ExtractorFn`, `TraversalContext`, `TraversalOptions`, `GlobalVars`, `StyleTypes`<br>• Functions & Presets: `extractFromDesign`, `simplifyRawFigmaObject`, `layoutExtractor`, `textExtractor`, `visualsExtractor`, `componentExtractor`, `allExtractors`, `layoutAndText`, `contentOnly`, `visualsOnly`, `layoutOnly`, `collapseSvgContainers`. |
| **`src/mcp-server.ts` → `dist/mcp-server.js`** | Module phụ cho tích hợp programmatic | **Programmatic MCP Server Entry**: Export các hàm khởi tạo và quản lý MCP server:<br>• `createServer` (`src/mcp/index.ts`)<br>• `getServerConfig`, `UsageError`, `type ServerConfig` (`src/config.ts`)<br>• `startServer`, `startHttpServer`, `stopHttpServer` (`src/server.ts`)<br>• `type FigmaService` (`src/services/figma.ts`). |

---

### 1.3. Luồng Khởi chạy Chi tiết từ `src/bin.ts`

```mermaid
flowchart TD
    A["Thực thi `figma-developer-mcp` (`src/bin.ts`)"] --> B["`cleye` parse CLI flags & commands"]
    B --> C{"`argv.command === 'fetch'`?"}
    
    C -- "Có (Chế độ CLI One-shot)" --> D["`src/commands/fetch.ts`"]
    D --> D1["1. `parseFigmaUrl(url)` + merge `--file-key`, `--node-id`"]
    D1 --> D2["2. `loadEnvFile` → `resolveAuth` → `requireGlobalCredentials`"]
    D2 --> D3["3. `initTelemetry({ immediateFlush: true })`"]
    D3 --> D4["4. `getFigmaData(...)` với format mặc định `yaml`"]
    D4 --> D5["5. `console.log(result.formatted)` & `shutdown()`"]

    C -- "Không (Chế độ MCP Server)" --> E["Hàm `main()` trong `src/bin.ts`"]
    E --> E1["Xác định `isStdio = flags.stdio === true || NODE_ENV === 'cli'`"]
    E1 --> E2["`getServerConfig({ ...argv.flags, stdio: isStdio })`"]
    E2 --> E3["`startServer(config)` trong `src/server.ts`"]
    
    E3 --> F{"`config.isStdioMode`?"}
    
    F -- "true (Stdio Mode)" --> G1["1. `requireGlobalCredentials(config.auth)` (Fail-fast)"]
    G1 --> G2["2. Cấu hình Undici Proxy Dispatcher & `initTelemetry`"]
    G2 --> G3["3. `createServer(config.auth, serverOptions)`"]
    G3 --> G4["4. `server.connect(new StdioServerTransport())`"]

    F -- "false (HTTP Mode)" --> H1["1. Cấu hình Undici Proxy Dispatcher & `initTelemetry`"]
    H1 --> H2["2. `startHttpServer(host, port, auth, serverOptions)` (Express 5)"]
    H2 --> H3["3. Lắng nghe `POST /mcp` và `POST /sse` (Stateless per-request)"]
```

- **Cơ chế xử lý lỗi khởi chạy (`src/bin.ts:67-76`)**:
  - Nếu lỗi ném ra là instance của `UsageError` (ví dụ: chạy `--stdio` nhưng quên truyền `FIGMA_API_KEY`), chương trình chỉ in thông báo `console.error(error.message)` sạch sẽ (không in stack trace gây rối) và thoát với `process.exit(1)`.
  - Mọi lỗi hệ thống khác sẽ in `"Failed to start server:"` kèm chi tiết đối tượng lỗi.

---

## 2. Hệ thống Cấu hình (`src/config.ts`)

### 2.1. Chuỗi Ưu tiên Phân giải (`resolve<T>`)
Mọi tùy chọn cấu hình trong `src/config.ts` đều được phân giải qua hàm generic `resolve<T>` theo thứ tự ưu tiên nghiêm ngặt:
**`CLI Flag` (`source: "cli"`) $\rightarrow$ `Environment Variable` (`source: "env"`) $\rightarrow$ `Default Value` (`source: "default"`)**.

```typescript
// src/config.ts:7-12 & 43-47
export type Source = "cli" | "env" | "default";

export interface Resolved<T> {
  value: T;
  source: Source;
}

export function resolve<T>(flag: T | undefined, env: T | undefined, fallback: T): Resolved<T> {
  if (flag !== undefined) return { value: flag, source: "cli" };
  if (env !== undefined) return { value: env, source: "env" };
  return { value: fallback, source: "default" };
}
```

**Nạp file `.env` (`loadEnvFile`, dòng 88–92)**:
- Trước khi đọc bất kỳ biến môi trường nào, `getServerConfig` gọi `dotenv.config({ path: envFilePath, override: true })`.
- Nếu người dùng truyền `--env <path>`, hệ thống nạp file đó; nếu không, mặc định tìm `.env` tại `process.cwd()`.
- Tùy chọn `override: true` đảm bảo các biến khai báo trong file `.env` sẽ ghi đè lên các biến trùng tên đang có trong `process.env`.

### 2.2. Bảng Tham chiếu Đầy đủ CLI Flags & Biến Môi trường

| Thuộc tính (`ServerConfig`) | CLI Flag (`cleye`) | Biến Môi trường (`process.env`) | Giá trị Mặc định | Chi tiết Kỹ thuật & Hành vi |
| :--- | :--- | :--- | :--- | :--- |
| **`auth.figmaApiKey`** | `--figma-api-key <string>` | `FIGMA_API_KEY` | `""` | Personal Access Token của Figma, được gửi qua HTTP header `X-Figma-Token`. |
| **`auth.figmaOAuthToken`** | `--figma-oauth-token <string>` | `FIGMA_OAUTH_TOKEN` | `""` | OAuth Bearer Token, được gửi qua HTTP header `Authorization: Bearer <token>`. |
| **`auth.useOAuth`** | *(Tự động suy diễn)* | *(Tự động suy diễn)* | `false` | Bằng `true` khi `figmaOAuthToken` khác rỗng. Khi cả hai cùng được cung cấp, **OAuth Token được ưu tiên hơn Personal Access Token**. |
| **`port`** | `--port <number>` | `FRAMELINK_PORT`, sau đó `PORT` | `3333` | Cổng lắng nghe ở chế độ HTTP. Đọc qua `envInt("FRAMELINK_PORT", "PORT")` (`FRAMELINK_PORT` có độ ưu tiên cao hơn `PORT`). |
| **`host`** | `--host <string>` | `FRAMELINK_HOST` | `"127.0.0.1"` | Địa chỉ IP/Host gắn với Express HTTP server. |
| **`outputFormat`** | `--format <string>` | `OUTPUT_FORMAT` | `"tree"` (Server)<br>`"yaml"` (`fetch` CLI) | Nhận 1 trong 3 giá trị: `"tree"`, `"yaml"`, `"json"`. Nếu truyền giá trị khác, `parseOutputFormat` sẽ ném lỗi ngay lập tức. |
| *(Gộp vào `outputFormat`)* | `--json` | *(Không có)* | `false` | Cờ tương thích ngược cho `--format=json`. Nếu truyền cả `--format` và `--json`, giá trị của `--format` sẽ được ưu tiên. |
| **`skipImageDownloads`** | `--skip-image-downloads` | `SKIP_IMAGE_DOWNLOADS` (`"true"`/`"false"`) | `false` | Khi `true`, MCP server sẽ **không đăng ký** công cụ `download_figma_images`. |
| **`imageDir`** | `--image-dir <string>` | `IMAGE_DIR` | `process.cwd()` | Luôn được chuẩn hóa thành đường dẫn tuyệt đối qua `path.resolve()`. Đóng vai trò là thư mục gốc sandbox cho mọi thao tác lưu ảnh. |
| **`proxy`** | `--proxy <string>` | `FIGMA_PROXY` | `undefined` | URL của HTTP/HTTPS proxy tường minh, hoặc chuỗi `"none"` để chủ động bỏ qua các biến `HTTP_PROXY`/`HTTPS_PROXY` của hệ thống. |
| **`isStdioMode`** | `--stdio` | `NODE_ENV=cli` | `false` | Bật chế độ giao tiếp qua `stdin`/`stdout` (`StdioServerTransport`). |
| **`noTelemetry`** | `--no-telemetry` | `FRAMELINK_TELEMETRY=off` hoặc `DO_NOT_TRACK` | `false` | Tắt hoàn toàn việc gửi telemetry ẩn danh về PostHog. |
| **`configSources`** | `--env <string>` | *(Không có)* | `./.env` | Bản ghi `Record<string, Source>` lưu lại nguồn gốc (`"cli"`, `"env"`, `"default"`) của từng tham số cấu hình để in log chẩn đoán. |

### 2.3. Bảo vệ Luồng `stdout` và Che giấu Token (`maskApiKey`)
- Ở chế độ HTTP (`isStdioMode === false`), `getServerConfig` in bảng tóm tắt cấu hình và nguồn gốc (`source`) của từng tham số ra `stdout`. Các khóa bí mật được đi qua hàm `maskApiKey(key)` chỉ hiển thị 4 ký tự cuối (`****abcd`).
- Ở chế độ Stdio (`isStdioMode === true`), `getServerConfig` **tắt hoàn toàn việc in ra `stdout`** vì trên giao thức MCP Stdio, mọi ký tự không phải gói tin JSON-RPC hợp lệ xuất hiện trên `stdout` đều sẽ làm hỏng kết nối với MCP client.
- Tuy nhiên, nếu chạy Stdio mà người dùng không cấu hình `--image-dir` / `IMAGE_DIR` (tức `configSources.imageDir === "default"`) và không tắt tải ảnh, server sẽ ghi một dòng cảnh báo ra **`stderr`** (`process.stderr.write`) nhắc người dùng cấu hình `--image-dir` vì `process.cwd()` của các tiến trình do IDE spawn thường rơi vào `/` hoặc `$HOME`.

---

## 3. Cơ chế MCP Server & Transport (`src/server.ts`, `src/mcp/index.ts`)

### 3.1. So sánh Chế độ `Stdio` và `StreamableHTTP` (`/mcp` & `/sse`)

| Tiêu chí | Stdio Mode (`StdioServerTransport`) | HTTP Mode (`StreamableHTTPServerTransport`) |
| :--- | :--- | :--- |
| **Cách kích hoạt** | `--stdio` hoặc `NODE_ENV=cli` | Mặc định khi chạy `figma-developer-mcp` không có `--stdio` |
| **Kiểm tra Auth lúc khởi động** | **Bắt buộc (`requireGlobalCredentials`)**: Nếu thiếu cả `FIGMA_API_KEY` và `FIGMA_OAUTH_TOKEN`, ném `UsageError` và dừng ngay. | **Không bắt buộc lúc khởi động**: Cho phép khởi chạy server không cần global token vì client có thể gửi token theo từng HTTP request. |
| **Mô hình Quản lý Instance** | **Stateful Singleton**: Tạo 1 `McpServer` và 1 `StdioServerTransport` duy nhất trong suốt vòng đời tiến trình. | **Stateless Per-Request**: Mỗi request `POST /mcp` hoặc `POST /sse` khởi tạo một cặp `McpServer` + `StreamableHTTPServerTransport({ sessionIdGenerator: undefined })` mới và đóng ngay khi `res.on("close")`. |
| **Đường dẫn (Endpoints)** | `process.stdin` / `process.stdout` | Hỗ trợ song song cả **`/mcp`** và **`/sse`** với cùng một handler `handlePost`:<br>• `POST /mcp`, `POST /sse` → Xử lý JSON-RPC request<br>• `GET /mcp`, `GET /sse` → Trả về `405 Method Not Allowed` (`Allow: POST`)<br>• `DELETE /mcp`, `DELETE /sse` → Trả về `405 Method Not Allowed` (`Allow: POST`) |
| **Tương thích ngược `/sse`** | Không áp dụng | Nhiều cấu hình client cũ vẫn trỏ URL vào `/sse`. Do các MCP client hiện đại luôn thử gửi `POST` (StreamableHTTP) trước khi lùi về giao thức SSE cũ, việc gắn `handlePost` vào `/sse` giúp cấu hình cũ hoạt động mượt mà không cần state. |
| **Điều hướng Log (`Logger.isHTTP`)** | `Logger.isHTTP = false` → Mọi log đẩy ra `console.error` (`stderr`). | `Logger.isHTTP = true` → Log in ra `console.log` (`stdout`). |

---

### 3.2. Xác thực Theo Từng Request qua HTTP Headers (`resolveRequestAuth`)

Trong chế độ HTTP (`src/server.ts:143-165` và `211-246`), mỗi request `POST` có thể cung cấp thông tin xác thực riêng thông qua HTTP headers, cho phép một instance HTTP server phục vụ nhiều người dùng khác nhau:

1. **Trích xuất Header**:
   - `getRequestApiKey(req)`: Đọc header `X-Figma-Token` (không phân biệt hoa thường trong Express `req.headers["x-figma-token"]`), cắt khoảng trắng `.trim()`.
   - `getRequestBearerToken(req)`: Đọc header `Authorization`, kiểm tra regex `/^Bearer\s+(.+)$/i` và lấy phần token phía sau.
2. **Thứ tự ưu tiên trong `resolveRequestAuth(baseAuth, requestKey, requestBearerToken)`**:
   - **Ưu tiên 1**: Nếu request có header `X-Figma-Token` (`requestKey`) $\rightarrow$ Dùng Personal Access Token của request (`{ figmaApiKey: requestKey, figmaOAuthToken: "", useOAuth: false }`).
   - **Ưu tiên 2**: Nếu request có header `Authorization: Bearer` (`requestBearerToken`) $\rightarrow$ Dùng OAuth token của request (`{ figmaApiKey: "", figmaOAuthToken: requestBearerToken, useOAuth: true }`).
   - **Ưu tiên 3**: Nếu request không gửi cả 2 header trên $\rightarrow$ Fallback về `baseAuth` (được cấu hình qua CLI/env lúc khởi động server).
3. **Cô lập Bí mật theo Ngữ cảnh Bất đồng bộ (`telemetry.withRequestSecrets`)**:
   - Mảng `[requestKey, requestBearerToken]` được truyền vào `telemetry.withRequestSecrets(requestSecrets, async () => { ... })` (sử dụng `AsyncLocalStorage` của Node.js) để nếu có lỗi xảy ra trong phạm vi request đó, hệ thống telemetry sẽ tự động che (redact) các token của request trước khi gửi báo cáo lỗi.

---

### 3.3. Cơ chế Progress Notification & Heartbeat (`src/mcp/progress.ts`)

Các file Figma lớn có thể mất từ 10 đến 55 giây để tải qua mạng và rút gọn hàng chục nghìn node, dễ khiến MCP client hiểu nhầm là server bị treo và ngắt kết nối (timeout). Module `src/mcp/progress.ts` giải quyết triệt để vấn đề này:

1. **Gửi thông báo tiến độ theo giai đoạn (`sendProgress`)**:
   - Kiểm tra `const progressToken = extra._meta?.progressToken;`. Nếu client không yêu cầu theo dõi tiến độ (`progressToken === undefined`), hàm lập tức bỏ qua (no-op).
   - Nếu có `progressToken`, gửi thông báo chuẩn MCP:
     ```typescript
     await extra.sendNotification({
       method: "notifications/progress",
       params: { progressToken, progress, total, message },
     });
     ```
2. **Nhịp tim giữ kết nối (`startProgressHeartbeat`)**:
   - Cứ mỗi `3000ms` (`intervalMs = 3_000`), bộ đếm `tick` tăng lên 1 và phát một bản tin `notifications/progress` với `progress: tick` (không truyền `total`).
   - Tham số `message` có thể là một chuỗi tĩnh (`"Waiting for Figma API response"`) hoặc một **hàm động** `() => string`. Trong giai đoạn `simplify`, tool truyền hàm `() => \`Simplifying design data (${progress.getNodeCount()} nodes processed)\`` để cập nhật trực tiếp số lượng node đã xử lý sau mỗi 100 nodes nhường event loop!
3. **Chống Race Condition khi Kết thúc (`await lastSend`)**:
   - Hàm dừng heartbeat được thiết kế bất đồng bộ:
     ```typescript
     return async () => {
       clearInterval(interval);
       await lastSend;
     };
     ```
   - Việc `await lastSend` đảm bảo rằng nếu một gói tin progress vừa được kích hoạt ngay trước khi tác vụ chính hoàn tất, server sẽ đợi gói tin notification đó gửi xong xuôi rồi mới trả kết quả tool call về cho client (tránh lỗi gửi notification cho một `progressToken` đã đóng).

---

### 3.4. Bắt lỗi Xác thực Đầu vào & Làm sạch Thông báo cho LLM (`src/mcp/validation-capture.ts`)

Mặc định, `@modelcontextprotocol/sdk` kiểm tra tham số đầu vào với Zod schema bên trong hàm nội bộ `validateToolInput` **trước khi** gọi vào hàm xử lý của tool. Nếu LLM truyền sai định dạng (ví dụ: `nodeId` sai cú pháp hoặc `fileName` thiếu đuôi `.png`), SDK ném `McpError(ErrorCode.InvalidParams)` chứa chuỗi mảng JSON lỗi rất dài của Zod, vừa tốn token vừa khó đọc đối với LLM.

Hàm `installValidationRejectCapture(server, context)` can thiệp thông minh ở cấp instance:
1. **Instance-level Patching (Fail-Open)**:
   - Ghi đè phương thức `validateToolInput` trực tiếp trên đối tượng `server` (không sửa `McpServer.prototype`). Nếu phiên bản SDK tương lai thay đổi cấu trúc nội bộ khiến `validateToolInput` không tồn tại, hàm tự động rút lui an toàn (`if (typeof patchable.validateToolInput !== "function") return;`).
2. **Trích xuất `ZodIssue` và Chuẩn hóa Đường dẫn (`normalizeFieldPath`)**:
   - Khi `original.call(this, tool, args, toolName)` ném `McpError` với mã `ErrorCode.InvalidParams`, hệ thống chạy lại `schema.safeParseAsync(args)` để lấy lỗi đầu tiên `issue = result.error?.issues?.[0]`.
   - Hàm `normalizeFieldPath(issue.path)` biến mọi chỉ số mảng kiểu số thành `"[]"`. Ví dụ: lỗi tại `["nodes", 0, "fileName"]` hay `["nodes", 3, "fileName"]` đều được chuẩn hóa thành `"nodes[].fileName"`, còn lỗi ở cấp gốc trả về `"(root)"`.
3. **Viết lại thông báo lỗi ngắn gọn cho LLM**:
   - Thay vì ném chuỗi JSON thô của SDK, hàm ném ra một `Error` mới cô đọng để LLM dễ dàng tự sửa (self-correct) ở lượt gọi kế tiếp:
     ```typescript
     throw new Error(`Invalid ${normalizeFieldPath(issue.path)}: ${issue.message}`);
     // Ví dụ: "Invalid nodes[].fileName: File names must contain only letters, numbers, underscores, dots, or hyphens, and end with .png, .svg, or .gif."
     ```

---

## 4. Đặc tả Chi tiết 2 MCP Tools (`src/mcp/tools/`)

### 4.1. Tool `get_figma_data` (`src/mcp/tools/get-figma-data-tool.ts`)
- **Mục đích**: Tải dữ liệu thiết kế của toàn bộ file Figma hoặc một node cụ thể, rút gọn cấu trúc (layout, text, visuals, components) và tuần tự hóa theo `outputFormat` (`tree` mặc định, `yaml`, hoặc `json`).
- **Annotations**: `{ readOnlyHint: true }` (luôn được đăng ký).
- **Schema Tham số Đầu vào (Zod)**:
  | Tên tham số | Kiểu & Ràng buộc | Bắt buộc? | Mô tả & Hành vi |
  | :--- | :--- | :---: | :--- |
  | **`fileKey`** | `z.string().regex(/^[a-zA-Z0-9]+$/)` | Có | Khóa định danh của file Figma (nằm trên URL `figma.com/(file\|design)/<fileKey>/...`). |
  | **`nodeId`** | `z.string().regex(/^I?\d+[:\|-]\d+(?:;\d+[:\|-]\d+)*$/).optional()` | Không | Định danh của node cần lấy. Chấp nhận cả dấu `:` lẫn `-` (ví dụ `1234:5678` hoặc `1234-5678`), cũng như đường dẫn instance lồng nhau nối bằng dấu `;` có tiền tố `I` (`I5666:180910;1:10515;1:10336`). Trước khi gọi API, mọi dấu `-` được chuẩn hóa thành `:`. |
  | **`depth`** | `z.number().optional()` | Không | Giới hạn độ sâu duyệt cây. Mô tả của tool dặn rõ LLM: *"OPTIONAL. Do NOT use unless explicitly requested by the user."* |

---

### 4.2. Tool `download_figma_images` (`src/mcp/tools/download-figma-images-tool.ts`)
- **Mục đích**: Tải các hình ảnh SVG, PNG, hoặc GIF từ file Figma về thư mục cục bộ của dự án, tự động cắt ảnh (crop) theo ma trận biến đổi và cung cấp kích thước/biến CSS.
- **Annotations**: `{ openWorldHint: true }`. Chỉ được đăng ký khi `!options.skipImageDownloads`.
- **Schema Tham số Đầu vào (Zod)**:
  - **`fileKey`** (`string`, alphanumeric): Khóa file Figma.
  - **`localPath`** (`string`): Đường dẫn thư mục lưu ảnh (tương đối so với `imageDir`, hoặc tuyệt đối nhưng bắt buộc phải nằm bên trong `imageDir`).
  - **`pngScale`** (`number`, mặc định `2`): Hệ số nhân độ phân giải khi xuất ảnh PNG.
  - **`nodes`** (`Array<Object>`): Danh sách các ảnh cần tải:
    - `nodeId` (`string`, bắt buộc): ID của node hình ảnh/icon (`1234:5678` hoặc `I5666:...`).
    - `imageRef` (`string`, tùy chọn): Hash tham chiếu của ảnh fill tĩnh. Để trống nếu tải vector `IMAGE-SVG`, ảnh GIF động, hoặc khi node có image fill nhưng `imageRef` bị `null`.
    - `gifRef` (`string`, tùy chọn): Hash tham chiếu cho ảnh GIF động (được ưu tiên hơn `imageRef`).
    - `fileName` (`string`, bắt buộc): Tên file lưu xuống đĩa, phải khớp regex `/^[a-zA-Z0-9_.-]+\.(png|svg|gif)$/`.
    - `needsCropping` (`boolean`, tùy chọn): Cờ báo hiệu ảnh cần được cắt theo `cropTransform`.
    - `cropTransform` (`number[][]`, tùy chọn): Ma trận biến đổi affine $2 \times 3$ từ Figma.
    - `requiresImageDimensions` (`boolean`, tùy chọn): Yêu cầu tính toán biến CSS `--original-width` và `--original-height` (dùng cho chế độ `TILE`).
    - `filenameSuffix` (`string`, tùy chọn, khớp `/^[a-zA-Z0-9_-]+$/`): Hậu tố băm 6 ký tự nối vào tên file khi cùng một `imageRef` được crop theo nhiều cách khác nhau.
- **Định dạng Kết quả Trả về**:
  ```text
  Downloaded 2 images to `/abs/path/to/project/public/images`:
  - hero-bg-3a9f1c.png: 800x600 | --original-width: 1200px; --original-height: 900px; (cropped)
  - icon-check.svg: 24x24 (also requested as: icon-success.svg)
  ```

---

## 5. Công cụ CLI Trực tiếp: Subcommand `fetch` (`src/commands/fetch.ts`)

Ngoài việc chạy làm MCP Server, binary `figma-developer-mcp` cung cấp lệnh con `fetch` để lập trình viên hoặc các script tự động hóa lấy dữ liệu thiết kế trực tiếp từ dòng lệnh và pipe qua các công cụ như `jq`, `yq` hoặc lưu thành file:

```bash
# Cú pháp tổng quát
figma-developer-mcp fetch [url] [flags]
```

### Đặc điểm Kỹ thuật của `fetch` Subcommand:
1. **Phân giải URL & Cờ linh hoạt (`src/commands/fetch.ts:83-100`)**:
   - Người dùng có thể truyền trực tiếp URL Figma làm đối số đầu tiên (`positionals[0]`), hàm `parseFigmaUrl(url)` sẽ tự động bóc tách `fileKey` và `nodeId`.
   - Nếu người dùng truyền thêm `--file-key` hoặc `--node-id`, giá trị của cờ tường minh sẽ **ghi đè** giá trị bóc từ URL (`fileKey ??= parsed.fileKey; nodeId ??= parsed.nodeId;`).
   - Ngay cả khi URL truyền vào bị lỗi cú pháp nhưng đã có `--file-key`, lỗi parse URL sẽ được bỏ qua.
2. **Định dạng đầu ra mặc định là `yaml`**:
   - Khác với MCP Server (mặc định dùng `"tree"` cho LLM), lệnh `fetch` trên CLI mặc định xuất `"yaml"` (hoặc `"json"` khi truyền `--json` / `--format=json`, hoặc `"tree"` khi truyền `--format=tree`) để tương thích tốt với các công cụ xử lý văn bản trên shell.
3. **Quản lý Vòng đời Tiến trình Ngắn hạn**:
   - Khởi tạo telemetry với `immediateFlush: true` và luôn gọi `shutdown()` trong khối `.finally()` để đảm bảo tiến trình thoát gọn gàng mà không làm treo event loop.
