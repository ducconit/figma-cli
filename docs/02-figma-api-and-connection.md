# 02. Kết nối Figma REST API, Xác thực, Proxy & Xử lý Lỗi (`Figma-Context-MCP`)

Tài liệu này phân tích chi tiết tầng giao tiếp mạng và tích hợp Figma REST API v1 trong `Figma-Context-MCP`, bao gồm lớp `FigmaService`, cơ chế xác thực kép (Personal Access Token vs OAuth Bearer Token), các endpoints được gọi, hạ tầng HTTP & Corporate Proxy (`undici`), hệ thống phân loại lỗi có cấu trúc (`ErrorMeta`), xử lý lỗi Rate Limit (429) / Forbidden (403), và bộ phân tích URL Figma.

---

## 1. Lớp `FigmaService` & Cơ chế Xác thực Kép (`src/services/figma.ts`)

### 1.1. Khởi tạo và Ưu tiên Xác thực (`FigmaAuthOptions`)

Lớp [`FigmaService`](file:///tmp/Figma-Context-MCP/src/services/figma.ts#L27-L343) là client trung tâm chịu trách nhiệm cho mọi giao tiếp với `https://api.figma.com/v1`. Khi khởi tạo, nó nhận đối tượng `FigmaAuthOptions`:

```typescript
// src/services/figma.ts:14-25
export type FigmaAuthOptions = {
  figmaApiKey: string;
  figmaOAuthToken: string;
  useOAuth: boolean;
};
```

Trong `constructor` (`src/services/figma.ts:32-36`):
```typescript
this.apiKey = figmaApiKey || "";
this.oauthToken = figmaOAuthToken || "";
this.useOAuth = !!useOAuth && !!this.oauthToken;
```

Phương thức `getAuthHeaders()` (`src/services/figma.ts:38-52`) quyết định header gửi đi theo 3 bước kiểm tra:
1. **Chế độ OAuth (`this.useOAuth === true`)**:
   - Đòi hỏi cả cờ `useOAuth` bật **và** chuỗi `oauthToken` không rỗng.
   - Trả về header chuẩn OAuth2:
     ```json
     { "Authorization": "Bearer <figmaOAuthToken>" }
     ```
2. **Kiểm tra Thiếu Thông tin Xác thực (`!this.useOAuth && !this.apiKey`)**:
   - Ném lỗi ngay lập tức ở phía client mà không thực hiện cuộc gọi mạng vô ích:
     `"Figma API authentication is required. Configure FIGMA_API_KEY or FIGMA_OAUTH_TOKEN on the server, or send X-Figma-Token / Authorization: Bearer on the HTTP request."`
3. **Chế độ Personal Access Token (`this.apiKey` có giá trị)**:
   - Trả về header riêng của Figma:
     ```json
     { "X-Figma-Token": "<figmaApiKey>" }
     ```

---

### 1.2. Toàn bộ các Endpoints của Figma REST API v1 Được Sử dụng

`FigmaService` đóng gói **5 phương thức gọi API** tương ứng với 4 endpoints của Figma REST API v1:

| Phương thức trong `FigmaService` | HTTP Endpoint (`https://api.figma.com/v1...`) | Query Parameters | Kiểu dữ liệu trả về (`@figma/rest-api-spec`) | Mục đích & Xử lý Đặc biệt |
| :--- | :--- | :--- | :--- | :--- |
| **`getRawFile(fileKey, depth?)`**<br>*(dòng 309–320)* | `GET /v1/files/:fileKey` | `depth` *(tùy chọn: chỉ gắn `?depth=${depth}` khi có truyền)* | `Promise<{ data: GetFileResponse; rawSize: number }>` | Tải toàn bộ cấu trúc cây tài liệu của một file Figma. Ghi log debug ra `logs/figma-raw.json` khi `NODE_ENV=development`. |
| **`getRawNode(fileKey, nodeId, depth?)`**<br>*(dòng 328–342)* | `GET /v1/files/:fileKey/nodes` | • `ids=${nodeId}` *(bắt buộc)*<br>• `depth=${depth}` *(tùy chọn)* | `Promise<{ data: GetFileNodesResponse; rawSize: number }>` | Chỉ tải nhánh cây con của một (hoặc nhiều) node cụ thể cùng bảng `components`, `componentSets`, `styles` liên quan. Ghi log debug ra `logs/figma-raw.json`. |
| **`getImageFillUrls(fileKey)`**<br>*(dòng 122–126)* | `GET /v1/files/:fileKey/images` | *(Không có)* | `Promise<Record<string, string>>` (trích từ `response.meta.images`) | Trả về bảng ánh xạ từ mã hash `imageRef` / `gifRef` sang URL tải file ảnh gốc trên S3/CDN của Figma cho tất cả các image fills có trong file. |
| **`getNodeRenderUrls(fileKey, nodeIds, "png", options)`**<br>*(dòng 133–145)* | `GET /v1/images/:fileKey` | • `ids=${nodeIds.join(",")}`<br>• `format=png`<br>• `scale=${options.pngScale \|\| 2}` | `Promise<Record<string, string>>` (lọc bỏ giá trị `null` từ `response.images`) | Yêu cầu máy chủ Figma kết xuất (render) danh sách các `nodeId` thành ảnh bitmap PNG với hệ số nhân độ phân giải `scale` (mặc định `2x`). |
| **`getNodeRenderUrls(fileKey, nodeIds, "svg", options)`**<br>*(dòng 146–156)* | `GET /v1/images/:fileKey` | • `ids=${svgIds.join(",")}`<br>• `format=svg`<br>• `svg_outline_text=true`<br>• `svg_include_id=false`<br>• `svg_simplify_stroke=true` | `Promise<Record<string, string>>` (lọc bỏ giá trị `null` từ `response.images`) | Yêu cầu máy chủ Figma kết xuất danh sách các `nodeId` (thường là các node `IMAGE-SVG` đã thu gọn) thành file vector `.svg`. Các tham số SVG được xây dựng qua `buildSvgQueryParams()` dùng `URLSearchParams`. |

---

## 2. Hạ tầng HTTP, Đo kích thước Payload & Hỗ trợ Corporate Proxy

### 2.1. Hàm `fetchJSON` & Phòng vệ Chiều sâu (`src/utils/fetch-json.ts`)

Mọi lời gọi API JSON trong `FigmaService` đều đi qua `requestWithSize<T>(endpoint)` $\rightarrow$ [`fetchJSON<T>(url, options)`](file:///tmp/Figma-Context-MCP/src/utils/fetch-json.ts#L56-L113):

1. **Đo kích thước byte gốc (`rawSize`) trước khi Parse JSON**:
   ```typescript
   const text = await response.text();
   const rawSize = Buffer.byteLength(text, "utf8");
   const data = JSON.parse(text) as T;
   return { data, rawSize };
   ```
   Việc đọc chuỗi văn bản và tính `Buffer.byteLength(text, "utf8")` giúp hệ thống đo chính xác dung lượng phản hồi thô từ Figma API (phục vụ báo cáo tỷ lệ nén `raw_size_kb` vs `simplified_size_kb`) mà không phải `JSON.stringify` ngược lại đối tượng đã parse.

2. **Chống rò rỉ Token trong Trang báo lỗi của Proxy (`redactFromResponseBody`)**:
   - Khi `!response.ok`, `fetchJSON` đọc nội dung body của phản hồi lỗi qua `readResponseBody(response, redactSecrets)` (`src/utils/fetch-json.ts:119-144`).
   - Một số tường lửa doanh nghiệp (như Zscaler, Cloudflare Access) khi chặn request thường in lại toàn bộ HTTP request headers (bao gồm cả `X-Figma-Token` hoặc `Authorization`) vào trong nội dung trang HTML báo lỗi!
   - Để phòng vệ chiều sâu (defense-in-depth), `FigmaService` luôn truyền `[this.apiKey, this.oauthToken]` vào `redactFromResponseBody`. Hàm `readResponseBody` sẽ:
     - Nén toàn bộ khoảng trắng xuống dòng (`text.replace(/\s+/g, " ").trim()`) về 1 dòng đơn.
     - Thay thế mọi sự xuất hiện của `apiKey` / `oauthToken` bằng chuỗi `"[REDACTED]"`.
     - Cắt ngắn tối đa ở `MAX_RESPONSE_BODY_CHARS = 500` ký tự (`"… [truncated]"`).

3. **Lưu trữ Headers & Body trong `HttpError`**:
   - Khi HTTP status không thành công (`!response.ok`), `fetchJSON` khởi tạo đối tượng `HttpError` lưu giữ `responseHeaders` (đã chuyển toàn bộ tên header về chữ thường) và `responseBody` đã làm sạch, giúp tầng xử lý lỗi phía trên đọc được các header chẩn đoán của Figma.

---

### 2.2. Quản lý Corporate Proxy với `undici` (`src/utils/proxy-env.ts`, `src/server.ts`)

Node.js tích hợp sẵn `fetch` dựa trên thư viện `undici`, nhưng mặc định `undici` **không tự động đọc** các biến môi trường `HTTP_PROXY` hay `HTTPS_PROXY`. Để hỗ trợ môi trường mạng doanh nghiệp, `Figma-Context-MCP` thiết lập global dispatcher cho `undici` tại `src/server.ts:45-51` kết hợp với `src/utils/proxy-env.ts`:

```mermaid
flowchart LR
    Start["Khởi động Server (`startServer`)"] --> CheckProxy{"Kiểm tra `config.proxy`\n(`--proxy` / `FIGMA_PROXY`)"}
    CheckProxy -- "Có giá trị & !== 'none'" --> Explicit["`setGlobalDispatcher(new ProxyAgent(config.proxy))`\n`setProxyMode('explicit')`"]
    CheckProxy -- "Không truyền `config.proxy`" --> CheckEnv{"`hasProxyEnv()`?\n(`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`)"}
    CheckEnv -- "Có biến môi trường" --> EnvProxy["`setGlobalDispatcher(new EnvHttpProxyAgent())`\n`setProxyMode('env')`"]
    CheckEnv -- "Không có" --> Direct["Dùng Default Dispatcher trực tiếp\n`proxyMode() === 'none'`"]
    CheckProxy -- "`config.proxy === 'none'`" --> Direct
```

- **Kiểm tra không phân biệt hoa thường (`hasProxyEnv`)**: Kiểm tra cả chữ hoa lẫn chữ thường (`HTTP_PROXY`/`http_proxy`, `HTTPS_PROXY`/`https_proxy`, `NO_PROXY`/`no_proxy`).
- **Tôn trọng `NO_PROXY`**: Khi dùng `EnvHttpProxyAgent()`, `undici` tự động bỏ qua proxy cho các tên miền nằm trong danh sách `NO_PROXY`.
- **Cơ chế thoát hiểm `--proxy=none`**: Nếu máy trạm của lập trình viên có sẵn biến môi trường `HTTPS_PROXY` cũ hoặc bị cấu hình sai gây lỗi 403/timeout khi gọi `api.figma.com`, người dùng chỉ cần truyền `--proxy=none` (hoặc `FIGMA_PROXY=none`) để buộc server kết nối trực tiếp mà không cần xóa biến môi trường của hệ thống.

---

## 3. Hệ thống Gắn nhãn Lỗi (`ErrorMeta`) & Chẩn đoán Lỗi 429 / 403

### 3.1. Gắn nhãn Lỗi bằng Symbol (`src/utils/error-meta.ts`)

Thay vì tạo hàng chục lớp `Error` con làm phức tạp việc kiểm tra kiểu qua nhiều tầng `try/catch`, hệ thống dùng một `Symbol` toàn cục ẩn `const META = Symbol.for("framelink.errorMeta")` để đính kèm siêu dữ liệu có cấu trúc (`ErrorMeta`) trực tiếp vào bất kỳ đối tượng `Error` nào:

```typescript
// src/utils/error-meta.ts:13-39
export type ErrorPhase = "validate" | "fetch" | "simplify" | "serialize" | "download" | "format_response";
export type ErrorCategory = "rate_limit" | "auth" | "not_found" | "invalid_input" | "network" | "figma_api" | "image_download" | "internal";

export type ErrorMeta = {
  phase?: ErrorPhase;
  category?: ErrorCategory;
  http_status?: number;
  network_code?: string;
  fs_code?: string;
  is_retryable?: boolean;
};
```

- **Hàm `tagError(error, meta): never`**: Gắn `meta` vào `error[META]` rồi **ném lại chính đối tượng `error` đó**.
- **Hàm `getErrorMeta(error): ErrorMeta`**: Duyệt chuỗi nguyên nhân `error` $\rightarrow$ `error.cause` $\rightarrow$ `error.cause.cause`... và hợp nhất các thuộc tính `ErrorMeta`. Quy tắc hợp nhất là **Outer Wins** (thuộc tính gắn ở tầng ngoài như `phase: "fetch"` sẽ không bị ghi đè bởi tầng dưới, trong khi các thuộc tính chi tiết ở tầng dưới như `http_status: 429`, `category: "rate_limit"`, `is_retryable: true` vẫn được giữ nguyên).

---

### 3.2. Phân tích Chi tiết Lỗi Rate Limit (HTTP 429) (`src/services/errors/rate-limit.ts`)

Khi Figma API trả về mã `429 Too Many Requests`, hàm [`buildRateLimitMessage(error)`](file:///tmp/Figma-Context-MCP/src/services/errors/rate-limit.ts#L12-L38) đọc `error.responseHeaders` từ `HttpError` để giải thích rõ lý do bị giới hạn và thời gian cần chờ dựa trên 4 headers đặc thù của Figma:

| Header phản hồi từ Figma | Ý nghĩa trong Figma API | Chuỗi thông báo được ghép thêm |
| :--- | :--- | :--- |
| **`Retry-After`** | Số giây cần chờ trước khi gửi lại request. | `" Retry after ${retryAfter} seconds."` |
| **`X-Figma-Rate-Limit-Type`** | Cấp độ giới hạn theo quyền của tài khoản (`"low"` đối với Viewer / Collaborator seat). | Nếu `=== "low"`: `" Your Figma seat type (Viewer or Collaborator) has a lower API rate limit."` |
| **`X-Figma-Plan-Tier`** | Gói cước của tổ chức/nhóm (`"starter"`, `"student"`, `"pro"`, `"org"`, `"enterprise"`). | Nếu là `"starter"` hoặc `"student"`: `" Your ${planTier} plan has limited API access."` |
| **`X-Figma-Upgrade-Link`** | Đường dẫn nâng cấp gói hoặc seat do Figma trả về. | `" Upgrade: ${upgradeLink}"` |
| *(Luôn đính kèm)* | Tài liệu chính thức về Rate Limits của Figma. | `" See https://developers.figma.com/docs/rest-api/rate-limits/"` |

---

### 3.3. Phân tích Chi tiết Lỗi Forbidden (HTTP 403) (`src/services/errors/forbidden.ts`)

Khi gặp mã `403 Forbidden`, hàm [`buildForbiddenMessage(endpoint, error)`](file:///tmp/Figma-Context-MCP/src/services/errors/forbidden.ts#L31-L58) xây dựng một bản hướng dẫn xử lý sự cố toàn diện cho cả người dùng lẫn LLM:
1. **In nguyên văn `responseBody` đã làm sạch**: Vì Figma trả về nguyên nhân cụ thể trong JSON body (ví dụ: `"Invalid scope(s)"`, `"Token expired"`, hoặc `"File not exportable"`).
2. **Liệt kê 5 nguyên nhân phổ biến (`FORBIDDEN_CAUSES`)**:
   - Thiếu quyền (scopes) bắt buộc trên token: cần bật **`File content: Read`** và **`Dev resources: Read`**.
   - Token đã bị thu hồi, nhập sai, hoặc (đối với OAuth token) đã hết hạn.
   - Tài khoản gắn với token không có quyền mở file thiết kế này.
   - Cài đặt chia sẻ của file Figma chặn quyền copy/share/export của Viewer.
   - Một proxy trung gian / tường lửa / VPN của công ty đã chặn request trước khi tới máy chủ Figma.
3. **Chỉ dẫn trực tiếp cho LLM (`LLM_INSTRUCTIONS`)**:
   `"Instructions: explain the specific reason from the response body above to the user in plain language and walk them through resolving it."`
4. **Gợi ý khắc phục Proxy theo ngữ cảnh (`PROXY_HINTS[proxyMode()]`)**:
   - Nếu `proxyMode() === "explicit"`: Báo cho người dùng biết server đang đi qua `--proxy` / `FIGMA_PROXY` và gợi ý tắt hoặc dùng `--proxy=none`.
   - Nếu `proxyMode() === "env"`: Báo cho người dùng biết server đã tự động nhận `HTTP_PROXY`/`HTTPS_PROXY` từ môi trường và gợi ý thêm `NO_PROXY=api.figma.com` hoặc truyền `--proxy=none`.

---

## 4. Bộ Phân tích URL Figma (`src/utils/figma-url.ts`)

Hàm [`parseFigmaUrl(input: string)`](file:///tmp/Figma-Context-MCP/src/utils/figma-url.ts#L11-L31) trích xuất `fileKey` và `nodeId` từ đường dẫn Figma mà người dùng dán vào:

```typescript
const FIGMA_PATH_PATTERN = /^\/(file|design)\/([a-zA-Z0-9]+)/;
```

1. **Kiểm tra Tên miền**: Sử dụng `new URL(input)` và chỉ chấp nhận `url.hostname === "figma.com"` hoặc kết thúc bằng `".figma.com"` (như `www.figma.com`). Nếu sai tên miền, ném lỗi `Not a Figma URL` với `category: "invalid_input"`.
2. **Các định dạng URL được hỗ trợ**:
   - Hỗ trợ: `https://www.figma.com/file/<fileKey>/...` và `https://www.figma.com/design/<fileKey>/...`.
   - **Không hỗ trợ**: Các đường dẫn `/proto/` (Prototype), `/board/` hoặc `/figjam/` (FigJam), `/slides/` (Figma Slides), `/make/`. Nếu người dùng truyền các đường dẫn này, hàm sẽ ném lỗi `Could not extract file key from Figma URL`.
3. **Chuẩn hóa `nodeId`**:
   - Lấy giá trị từ query parameter `url.searchParams.get("node-id")`.
   - Thay thế toàn bộ dấu gạch ngang `-` thành dấu hai chấm `:` (`.replace(/-/g, ":")`), ví dụ `node-id=12-34` trên trình duyệt được chuyển thành `12:34` để khớp với định dạng ID trong Figma REST API.

---

## 5. Điều phối Luồng `getFigmaData` & Thu thập Metrics (`src/services/get-figma-data*.ts`)

Hàm [`getFigmaData(figmaService, input, outputFormat, hooks)`](file:///tmp/Figma-Context-MCP/src/services/get-figma-data.ts#L73-L178) điều phối toàn bộ quy trình lấy dữ liệu qua **3 giai đoạn đo thời gian độc lập** (`fetch` $\rightarrow$ `simplify` $\rightarrow$ `serialize`):

1. **Giai đoạn `fetch`**:
   - Nếu có `input.nodeId`, gọi `figmaService.getRawNode(fileKey, nodeId, depth)`.
   - Nếu không có `input.nodeId`, gọi `figmaService.getRawFile(fileKey, depth)`.
   - Ghi nhận thời gian `fetchMs` và kích thước phản hồi gốc `rawSizeKb = rawResult.rawSize / 1024`.
2. **Giai đoạn `simplify`**:
   - Khởi tạo bộ đếm cục bộ `nodeCounter = { count: 0 }` truyền vào `simplifyRawFigmaObject(rawApiResponse, allExtractors, { maxDepth: depth, afterChildren: collapseSvgContainers, nodeCounter })`.
   - Ghi nhận thời gian `simplifyMs`.
3. **Giai đoạn Đo lường (`get-figma-data-metrics.ts`) & `serialize`**:
   - Đếm tổng số node thô (`countRawNodes`), số lượng Named Styles không trùng lặp (`countNamedStyles`), kiểm tra sự tồn tại của Figma Variables (`detectVariables` — dừng sớm ngay khi thấy node có `boundVariables`).
   - Duyệt cây rút gọn qua `measureSimplifiedDesign(simplifiedDesign)` (có giải tham chiếu `design.elements[node.template]` cho các node đã deduplicate) để thống kê: `simplifiedNodeCount`, `maxDepth`, `componentCount`, `instanceCount`, `textNodeCount`, `imageNodeCount`, `componentPropertyCount`.
   - Gọi `wrapForSerialization(simplifiedDesign)` và `serializeResult(..., outputFormat)`, đo `serializeMs` và `simplifiedSizeKb`.
4. **Đảm bảo Gọi `onComplete` trong `finally`**:
   - Dù thành công hay xảy ra lỗi ở bất kỳ giai đoạn nào, hook `hooks.onComplete` luôn được gọi đúng 1 lần trong khối `finally` (bọc trong `try/catch` riêng để lỗi telemetry không bao giờ ảnh hưởng đến luồng chính).
