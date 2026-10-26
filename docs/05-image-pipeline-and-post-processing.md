# 05. Pipeline Tải Ảnh, Khử Trùng Lặp, Cắt Ảnh Jimp & Bảo mật Đường dẫn (`Figma-Context-MCP`)

Tài liệu này phân tích chi tiết toàn bộ quy trình xử lý hình ảnh trong `Figma-Context-MCP`, từ bước nhận diện nguồn ảnh trong dữ liệu thiết kế (`src/transformers/style/image.ts`), giải quyết URL tải và khử trùng lặp (`src/services/download-figma-images.ts`, `src/services/figma.ts`), cắt ảnh bằng ma trận affine với `@jimp/core` (`src/utils/image-processing.ts`), tính toán biến CSS cho chế độ lặp `TILE`, cho đến cơ chế sandbox chống tấn công Path Traversal (`src/utils/local-path.ts`).

---

## 1. Phân loại 3 Nguồn Hình ảnh trong Figma (`imageRef`, `gifRef`, `nodeId`)

Khi LLM gọi công cụ `download_figma_images` (sau khi đã đọc cấu trúc thiết kế từ `get_figma_data`), hệ thống phân biệt 3 loại tài nguyên hình ảnh với cơ chế lấy URL từ Figma REST API hoàn toàn khác nhau:

| Loại Tài nguyên | Trường nhận diện trong `nodes[]` | Endpoint Figma API được gọi | Cơ chế Xử lý & Trường hợp Đặc biệt |
| :--- | :--- | :--- | :--- |
| **1. Static Image Fill** *(Ảnh bitmap tĩnh làm nền/lớp tô)* | Có `imageRef` (và không có `gifRef`) | `GET /v1/files/:fileKey/images`<br>*(Chỉ gọi 1 lần cho cả file)* | Tra cứu `imageRef` trong từ điển `response.meta.images` để lấy URL tải file ảnh gốc chưa qua nén lại.<br>**Ngoại lệ `imageRef: null`**: Khi ảnh được copy-paste từ một file Figma khác mà chủ sở hữu token không có quyền truy cập, Figma trả về `imageRef: null`. Khi đó `src/transformers/style/image.ts` tự động bỏ qua `imageRef`, buộc LLM tải ảnh bằng cách render chính `nodeId` đó! |
| **2. Animated GIF Fill** *(Ảnh động GIF)* | Có `gifRef` | `GET /v1/files/:fileKey/images` | Với ảnh GIF, Figma trả về cả `imageRef` (chỉ là 1 khung hình tĩnh) và `gifRef` (file GIF động gốc). Hệ thống **luôn ưu tiên `gifRef` trước `imageRef`** (`const fillRef = gifRef ?? imageRef;`) và **không chạy Jimp crop** trên file `.gif` để không làm hỏng các khung hình chuyển động. |
| **3. Rendered Node** *(Icon SVG, Vector đã collapse, Pattern source, hoặc Frame)* | Không có `imageRef` và không có `gifRef` (chỉ có `nodeId`) | `GET /v1/images/:fileKey` | Dựa vào phần mở rộng của `fileName` để chia thành 2 lô gọi API song song:<br>• **Đuôi `.svg`**: Gọi `format=svg&svg_outline_text=true&svg_include_id=false&svg_simplify_stroke=true`.<br>• **Đuôi `.png` / khác**: Gọi `format=png&scale=${pngScale}` (mặc định `pngScale = 2`). |

---

## 2. Cơ chế Khử Trùng Lặp (Deduplication) & Hậu tố `filenameSuffix`

Trong một màn hình thiết kế, cùng một bức ảnh avatar hoặc ảnh sản phẩm (`imageRef`) có thể được sử dụng lặp lại ở 10 thẻ `Card` khác nhau, hoặc được cắt (crop) theo 2 tỷ lệ khung hình khác nhau. Hàm [`downloadFigmaImages`](file:///tmp/Figma-Context-MCP/src/services/download-figma-images.ts#L78-L163) xử lý thông minh cả hai tình huống này:

```mermaid
flowchart TD
    Req["Mỗi phần tử `node` trong `params.nodes`"] --> Norm["1. Chuẩn hóa `nodeId` (`-` → `:`)\n2. Nếu có `filenameSuffix`, gắn vào tên file:\n`name.png` → `name-suffix.png`"]
    Norm --> CheckType{"Loại nguồn ảnh?"}
    
    CheckType -- "Có `gifRef` hoặc chỉ có `nodeId`" --> NewItem["Tạo mục tải mới trong `downloadItems`\n(Không dedup giữa các `nodeId` khác nhau)"]
    
    CheckType -- "Có `imageRef` (Static Fill)" --> MakeKey["Tạo khóa: `uniqueKey = ${imageRef}-${filenameSuffix || 'none'}`"]
    MakeKey --> CheckSeen{"`!filenameSuffix && seenDownloads.has(uniqueKey)`?"}
    
    CheckSeen -- "Đúng (Cùng ảnh gốc, không crop riêng)" --> Dedup["• KHÔNG tải lại lần thứ 2\n• Ghi tên file mới vào `downloadToRequests` của mục tải đã có\n• Kế thừa cờ `requiresImageDimensions` nếu bằng `true`\n• Báo lại cho LLM: `(also requested as: ...)`"]
    CheckSeen -- "Sai (Chưa tải hoặc có `filenameSuffix` crop riêng)" --> AddSeen["• Lưu `seenDownloads.set(uniqueKey, index)`\n• Thêm vào `downloadItems` để tải và crop độc lập"]
```

### Cách tạo `filenameSuffix` từ Ma trận Affine (`src/transformers/style/image.ts:161-174`):
Khi bước trích xuất (`parseImagePaint`) phát hiện một Image Fill có ma trận `imageTransform` khác ma trận đơn vị, hàm `generateTransformHash(transform)` nối 6 hệ số của ma trận thành chuỗi `"${a}_${b}_${c}_${d}_${e}_${f}"` và chạy hàm băm 32-bit (`acc = ((acc << 5) - acc + charCode) & 0xffffffff`) để sinh ra một mã hex 6 ký tự (ví dụ `"3a9f1c"`).
- Nếu 2 node dùng cùng một `imageRef` nhưng có 2 ma trận `imageTransform` khác nhau $\rightarrow$ chúng có 2 `filenameSuffix` khác nhau (`banner-3a9f1c.png` và `banner-8b2d0e.png`), được tải và cắt thành 2 file riêng biệt.
- Nếu nhiều node dùng cùng một `imageRef` không crop (`filenameSuffix` trống) $\rightarrow$ chỉ tải **1 lần duy nhất**, và trong phản hồi của MCP tool sẽ ghi rõ:
  `- avatar.png: 200x200 (also requested as: user-pic.png, comment-avatar.png)` để LLM biết dùng chung file `avatar.png`.

---

## 3. Toán học Cắt Ảnh (Image Cropping) với `@jimp/core` (`src/utils/image-processing.ts`)

### 3.1. Ma trận Biến đổi `imageTransform` của Figma
Khi người dùng chọn chế độ **Crop** cho một bức ảnh trong Figma (phóng to, thu nhỏ hoặc kéo dịch vùng nhìn thấy của ảnh bên trong khung), Figma lưu phép biến đổi này dưới dạng ma trận affine $2 \times 3$ trong không gian tọa độ chuẩn hóa $[0, 1] \times [0, 1]$:

$$\text{cropTransform} = \begin{bmatrix} \text{scaleX} & \text{skewX} & \text{translateX} \\ \text{skewY} & \text{scaleY} & \text{translateY} \end{bmatrix}$$

Trong đó:
- $\text{scaleX} = \text{cropTransform}[0][0]$: Tỷ lệ chiều rộng của vùng cắt so với chiều rộng ảnh gốc ($0 < \text{scaleX} \le 1$).
- $\text{translateX} = \text{cropTransform}[0][2]$: Tọa độ dịch chuyển ngang của góc trên-trái vùng cắt theo chiều rộng ảnh gốc ($0 \le \text{translateX} < 1$).
- $\text{scaleY} = \text{cropTransform}[1][1]$: Tỷ lệ chiều cao của vùng cắt so với chiều cao ảnh gốc ($0 < \text{scaleY} \le 1$).
- $\text{translateY} = \text{cropTransform}[1][2]$: Tọa độ dịch chuyển dọc của góc trên-trái vùng cắt theo chiều cao ảnh gốc ($0 \le \text{translateY} < 1$).

### 3.2. Công thức Tính Vùng Cắt Pixel trong `applyCropTransform` (`src/utils/image-processing.ts:16-63`)
Sau khi tải file ảnh gốc về đĩa, hàm `applyCropTransform(imagePath, cropTransform)` nạp ảnh bằng `Jimp.read(imagePath)` (sử dụng `@jimp/core` nhẹ, thuần JavaScript, hỗ trợ `png`, `jpeg`, `gif` mà không cần biên dịch native C++ như `sharp`) để lấy kích thước pixel thực tế $(W, H) = (\text{image.width}, \text{image.height})$.

Tọa độ hộp cắt $(\text{cropLeft}, \text{cropTop}, \text{cropWidth}, \text{cropHeight})$ được tính và kẹp biên (clamp) an toàn trong phạm vi bức ảnh:

$$\text{cropLeft} = \max\left(0,\ \text{round}(\text{translateX} \times W)\right)$$
$$\text{cropTop} = \max\left(0,\ \text{round}(\text{translateY} \times H)\right)$$
$$\text{cropWidth} = \min\left(W - \text{cropLeft},\ \text{round}(\text{scaleX} \times W)\right)$$
$$\text{cropHeight} = \min\left(H - \text{cropTop},\ \text{round}(\text{scaleY} \times H)\right)$$

Nếu $\text{cropWidth} > 0$ và $\text{cropHeight} > 0$, hàm gọi:
```typescript
image.crop({
  x: cropLeft,
  y: cropTop,
  w: cropWidth,
  h: cropHeight,
});
await image.write(imagePath);
```
File đã cắt sẽ ghi đè trực tiếp lên `imagePath` và trả về kích thước mới sau khi cắt.

---

## 4. Đo Kích thước SVG & Giải Bài toán CSS `TILE` Mode

### 4.1. Đọc Kích thước từ Mã nguồn SVG (`parseSvgDimensions`)
Vì Jimp chỉ đọc được ảnh bitmap (PNG/JPEG/GIF), đối với các file `.svg`, hàm [`parseSvgDimensions(svg)`](file:///tmp/Figma-Context-MCP/src/utils/image-processing.ts#L88-L120) phân tích trực tiếp thẻ mở `<svg ...>`:
1. **Ưu tiên 1**: Đọc thuộc tính `width="..."` và `height="..."`. Nếu cả hai tồn tại, không chứa ký tự `"%"`, và parse ra số thực dương $> 0$ $\rightarrow$ làm tròn và trả về `{ width, height }`.
2. **Ưu tiên 2 (Fallback sang `viewBox`)**: Nếu `width`/`height` bị khuyết hoặc dùng đơn vị phần trăm `"100%"`, hàm tách thuộc tính `viewBox="minX minY vbWidth vbHeight"` theo `/[\s,]+/` và lấy 2 giá trị cuối `(vbWidth, vbHeight)` làm kích thước chuẩn của icon SVG.

---

### 4.2. Sự Phối hợp 2 Bước cho Chế độ Lặp Ảnh `TILE` (`requiresImageDimensions`)

1. **Sự khác biệt giữa Figma và CSS**:
   - Khi một lớp phủ ảnh trong Figma đặt `scaleMode: "TILE"` kèm hệ số `scalingFactor` (ví dụ `0.5`), Figma nhân `0.5` với **kích thước gốc của file ảnh** (ví dụ ảnh $100 \times 100\text{px}$ sẽ lặp lại mỗi ô $50 \times 50\text{px}$).
   - Trong CSS, nếu viết `background-size: 50%`, trình duyệt lại tính `50%` của **khung chứa (container)** chứ không phải của file ảnh! Hơn nữa, ở bước `get_figma_data`, phản hồi JSON của Figma không hề cho biết file ảnh `imageRef` có kích thước gốc bao nhiêu pixel.
2. **Giải pháp Cầu nối 2 Bước**:
   - **Bước 1 (Trong `get_figma_data` — `src/transformers/style/image.ts:125-137`)**:
     Khi gặp `scaleMode === "TILE"`, bộ chuyển đổi phát ra công thức CSS sử dụng 2 biến CSS chưa biết giá trị:
     ```css
     background-repeat: repeat;
     background-size: calc(var(--original-width) * 0.5) calc(var(--original-height) * 0.5);
     ```
     Đồng thời gắn cờ `requiresImageDimensions: true` vào `imageDownloadArguments` của ảnh đó.
   - **Bước 2 (Trong `download_figma_images` — `src/utils/image-processing.ts:221-249`)**:
     Sau khi tải file ảnh về đĩa và đọc được kích thước thật $(W, H)$, nếu `requiresImageDimensions === true`, hàm `generateImageCSSVariables` sẽ sinh chuỗi khai báo biến CSS và trả về trực tiếp trong kết quả của tool:
     ```text
     - pattern-tile.png: 100x100 | --original-width: 100px; --original-height: 100px;
     ```
     LLM chỉ cần copy chuỗi `--original-width: 100px; --original-height: 100px;` đặt vào `style` của phần tử HTML là công thức `calc(...)` ở Bước 1 hoạt động chính xác tuyệt đối!

---

## 5. Bảo mật Đường dẫn: 3 Lớp Chống Path Traversal (`src/utils/local-path.ts`)

Vì `download_figma_images` là công cụ có quyền ghi file trực tiếp xuống máy trạm của lập trình viên, `Figma-Context-MCP` thiết lập **3 lớp kiểm soát bảo mật nghiêm ngặt**:

### Lớp 1: Kiểm tra `localPath` trong Sandbox `imageDir` (`resolveLocalPath`, `src/utils/local-path.ts:43-86`)
Mọi đường dẫn thư mục `localPath` do LLM truyền vào đều được kiểm tra so với thư mục gốc `baseDir` (`config.imageDir`, mặc định là `process.cwd()`):
1. **Chặn đường dẫn ổ đĩa Windows trên hệ thống POSIX (`drive_letter_on_posix`)**:
   - Trên Linux/macOS (`path.sep === "/"`), hàm `path.posix.isAbsolute("C:\\Users\\...")` trả về `false`. Nếu không chặn trước, Node.js sẽ coi `"C:\\..."` là một thư mục tương đối và tạo ra thư mục kỳ dị `/workspace/C:/Users/...`.
   - Do đó, trên POSIX, nếu `rawPath` khớp regex `/^[A-Za-z]:[/\\]/`, hàm lập tức từ chối với mã lỗi `"drive_letter_on_posix"`.
2. **Chuẩn hóa dấu gạch chéo và phân giải đường dẫn tuyệt đối**:
   - Đổi mọi dấu `\` thành `/` trên POSIX.
   - Nếu `normalized` là đường dẫn tuyệt đối (`path.isAbsolute`), giải quyết trực tiếp `candidate = path.resolve(normalized)`. Nếu là tương đối, giải quyết theo `base`: `candidate = path.resolve(base, normalized)`.
3. **Kiểm tra Bao hàm Ngữ nghĩa (`isWithin(base, candidate)`)**:
   ```typescript
   export function isWithin(base: string, candidate: string, pathImpl: PathImpl = path): boolean {
     const rel = pathImpl.relative(base, candidate);
     return rel === "" || (!rel.startsWith("..") && !pathImpl.isAbsolute(rel));
   }
   ```
   - Nếu `candidate` thoát ra ngoài `baseDir` (ví dụ truyền `../../etc` hoặc `/public/images` có dấu `/` ở đầu làm trỏ tới thư mục gốc `/` của hệ điều hành), hàm trả về `{ ok: false, reason: "outside_image_dir" }`.
   - Đặc biệt, nếu phát hiện LLM truyền nhầm dấu `/` ở đầu (như `"/src/assets"` thay vì `"src/assets"`), thông báo lỗi trả về sẽ hướng dẫn rõ:
     *'If you meant a directory inside the image directory, drop the leading slash (e.g., use "src/assets").'*

### Lớp 2: Ràng buộc Zod Schema trên `fileName` và `filenameSuffix`
- Trong `src/mcp/tools/download-figma-images-tool.ts`:
  - `fileName` bắt buộc khớp `/^[a-zA-Z0-9_.-]+\.(png|svg|gif)$/` (cấm tuyệt đối các ký tự `/` và `\`).
  - `filenameSuffix` bắt buộc khớp `/^[a-zA-Z0-9_-]+$/`.

### Lớp 3: Chốt chặn Cuối cùng trước khi Mở Stream Ghi File (`src/utils/common.ts:25-30`)
Ngay bên trong hàm tầng thấp `downloadFigmaImage(fileName, localPath, imageUrl)`, trước khi gọi `fs.createWriteStream(fullPath)`, hệ thống vẫn kiểm tra lại một lần nữa:
```typescript
const fullPath = path.resolve(path.join(localPath, fileName));
if (!isWithin(localPath, fullPath)) {
  tagError(new Error(`File path escapes target directory: ${fileName}`), {
    category: "invalid_input",
  });
}
```
Đảm bảo ngay cả khi hàm `downloadFigmaImage` được gọi trực tiếp từ code thư viện bên ngoài mà không đi qua MCP Zod schema, không một file nào có thể bị ghi ra ngoài thư mục đích.
