# 04. Hệ thống Transformers: Chuyển đổi Figma sang CSS / Web Semantics (`Figma-Context-MCP`)

Tài liệu này phân tích chi tiết toàn bộ các bộ chuyển đổi (Transformers) trong thư mục `src/transformers/` của `Figma-Context-MCP`. Nhiệm vụ của tầng này là dịch hệ tọa độ, bố cục Auto Layout, màu sắc, gradient, hình ảnh, văn bản đa định dạng (rich text), hiệu ứng và thuộc tính component đặc thù của Figma sang mô hình ngữ nghĩa chuẩn của Web (HTML/CSS Flexbox, CSS Grid, Markdown) với độ chính xác toán học cao và chi phí token thấp nhất.

---

## 1. Layout Transformers (`src/transformers/layout.ts`, `layout/flex.ts`, `layout/grid.ts`, `layout/common.ts`)

Điểm nhập chính là [`buildSimplifiedLayout(n, parent)`](file:///tmp/Figma-Context-MCP/src/transformers/layout.ts#L21-L34), kết hợp:
- `buildSimplifiedFrameValues(n)`: Thuộc tính bố cục của chính node đó với tư cách là một container chứa con.
- `buildSimplifiedLayoutValues(n, parent, mode, parentGridPacked)`: Thuộc tính kích thước và vị trí của node đó bên trong ngữ cảnh của `parent`.

### 1.1. Ánh xạ Chế độ Bố cục (`layoutModeToSchema`)
Đối với các node thuộc nhóm Frame (`FRAME`, `GROUP`, `COMPONENT`, `COMPONENT_SET`, `INSTANCE` — được nhận diện qua `isFrame(n)` kiểm tra thuộc tính boolean `clipsContent`):
- `layoutMode === "HORIZONTAL"` $\rightarrow$ `mode: "row"` (CSS Flexbox ngang)
- `layoutMode === "VERTICAL"` $\rightarrow$ `mode: "column"` (CSS Flexbox dọc)
- `layoutMode === "GRID"` $\rightarrow$ `mode: "grid"` (CSS Grid)
- `layoutMode === "NONE"` hoặc `undefined` $\rightarrow$ `mode: "none"`

---

### 1.2. Chuyển đổi Flexbox (`mode: "row" | "column"`) (`src/transformers/layout/flex.ts`)

| Thuộc tính Figma | Thuộc tính `SimplifiedLayout` (CSS) | Quy tắc Chuyển đổi & Tối ưu Token |
| :--- | :--- | :--- |
| **`primaryAxisAlignItems`** | **`justifyContent`** | • `"MIN"` $\rightarrow$ **`undefined`** (Lược bỏ vì `flex-start` là mặc định của CSS Flexbox)<br>• `"CENTER"` $\rightarrow$ `"center"`<br>• `"MAX"` $\rightarrow$ `"flex-end"`<br>• `"SPACE_BETWEEN"` $\rightarrow$ `"space-between"` |
| **`counterAxisAlignItems`** | **`alignItems`** | • **Quy tắc ưu tiên `allStretch`**: Kiểm tra các con in-flow (không phải `ABSOLUTE`). Nếu tất cả đều có kích thước trục phụ (`layoutSizingVertical` khi `row`, hoặc `layoutSizingHorizontal` khi `column`) bằng `"FILL"`, trả về ngay `"stretch"`.<br>• Ngược lại ánh xạ: `"MIN"` $\rightarrow$ **`undefined`**, `"CENTER"` $\rightarrow$ `"center"`, `"MAX"` $\rightarrow$ `"flex-end"`, `"BASELINE"` $\rightarrow$ `"baseline"`. |
| **`layoutAlign`** *(trên child)* | **`alignSelf`** | • `"MIN"` / `"INHERIT"` $\rightarrow$ **`undefined`**<br>• `"CENTER"` $\rightarrow$ `"center"`<br>• `"MAX"` $\rightarrow$ `"flex-end"`<br>• `"STRETCH"` $\rightarrow$ `"stretch"` |
| **`layoutWrap`** | **`wrap`** | • `"WRAP"` $\rightarrow$ `true` (còn lại lược bỏ thành `undefined`). |
| **`itemSpacing` & `counterAxisSpacing`** | **`gap`** | • Nếu `primaryAxisAlignItems === "SPACE_BETWEEN"`, Figma vẫn giữ số `itemSpacing` cũ trong JSON dù UI hiện "Auto" $\rightarrow$ hệ thống ép `primaryGap = undefined`.<br>• `counterGap` chỉ được đọc khi `layoutWrap === "WRAP"` và `counterAxisAlignContent !== "SPACE_BETWEEN"`.<br>• Quy đổi theo trục (`row` vs `column`) và rút gọn qua `gapShorthand(rowGap, colGap)` thành `"12px"` hoặc `"8px 16px"`. |

---

### 1.3. Chuyển đổi CSS Grid (`mode: "grid"`) (`src/transformers/layout/grid.ts`)

Figma hỗ trợ Auto Layout dạng Grid với các thuộc tính `gridColumnsSizing`, `gridRowsSizing`, `gridColumnAnchorIndex`, `gridRowAnchorIndex`, `gridColumnSpan`, `gridRowSpan`. Hệ thống xử lý 4 bài toán phức tạp khi dịch sang CSS Grid:

1. **Thuộc tính trên Grid Container**:
   - `gridTemplateColumns = n.gridColumnsSizing?.trim()`
   - `gridTemplateRows = n.gridRowsSizing?.trim()`
   - `gap = gapShorthand(n.gridRowGap, n.gridColumnGap)`
2. **Kiểm tra Lưới Đặc (`isPackedGrid`) vs Lưới Thưa (`!packed`)**:
   - Hàm [`isPackedGrid(children)`](file:///tmp/Figma-Context-MCP/src/transformers/layout/grid.ts#L62-L92) duyệt các phần tử con in-flow, đánh dấu mọi ô `(r, c)` được chiếm giữ vào một `Set<string>()`.
   - Nếu tổng số ô được đánh dấu bằng đúng diện tích hình chữ nhật bao `(maxRow + 1) * (maxCol + 1)` và không có 2 phần tử nào đè lên cùng 1 ô, lưới được kết luận là **đặc (`packed = true`)**.
   - **Khi `packed === true`**: Nhờ các con đã được `computeGridChildOrder` sắp xếp đúng theo thứ tự dòng/cột, trình duyệt tự động đặt chúng vào đúng ô. Hệ thống **bỏ qua tọa độ dòng/cột**, chỉ phát `gridColumn: "span ${colSpan}"` hoặc `gridRow: "span ${rowSpan}"` khi `span > 1`!
   - **Khi `packed === false` (có ô trống hoặc chồng lấp)**: Phát tọa độ 1-based tường minh của CSS Grid:
     $$\text{col} = \text{gridColumnAnchorIndex} + 1, \quad \text{row} = \text{gridRowAnchorIndex} + 1$$
     $$\text{gridColumn} = \text{colSpan} > 1 \ ? \ \text{"}\$\{\text{col}\}\text{ / span }\$\{\text{colSpan}\}\text{"} : \text{"}\$\{\text{col}\}\text{"}$$
3. **Căn chỉnh trong Ô (`justifySelf` & `alignSelf`)**:
   - `gridChildHorizontalAlign` (`MIN` / `CENTER` / `MAX`) $\rightarrow$ `justifySelf: "start" | "center" | "end"`.
   - `gridChildVerticalAlign` (`MIN` / `CENTER` / `MAX`) $\rightarrow$ `alignSelf: "start" | "center" | "end"`.
4. **Bảo toàn Thứ tự Chồng lớp (`zIndex`) khi Sắp xếp lại DOM**:
   - Nếu việc sắp xếp lại thứ tự con theo dòng/cột (`computeGridChildOrder`) làm thay đổi vị trí của một phần tử so với mảng gốc của Figma, **VÀ** kiểm tra hình chữ nhật bao `absoluteBoundingBox` thấy có ít nhất 2 phần tử in-flow giao nhau (`gridChildrenOverlap`), hệ thống tự động gắn `zIndex: originalIndex` để các ô đè lên nhau hiển thị đúng thứ tự lớp như trong Figma.

---

### 1.4. Kích thước (`sizing`, `dimensions`), Root `contextual` và Vị trí Tương đối (`locationRelativeToParent`)

1. **Ánh xạ `sizing`**:
   - `layoutSizingHorizontal` / `layoutSizingVertical`: `"FIXED"` $\rightarrow$ `"fixed"`, `"FILL"` $\rightarrow$ `"fill"`, `"HUG"` $\rightarrow$ `"hug"`.
2. **Xử lý Đặc biệt cho Node Gốc (`parent === undefined` — `layout.ts:115-124`)**:
   - Khi người dùng lấy một Frame gốc, nó không có `parent` trong ngữ cảnh nên Figma luôn trả về `layoutSizingHorizontal: "FIXED"`, `layoutSizingVertical: "FIXED"`. Nếu xuất thẳng `fixed` và `width: 1440, height: 900`, LLM sẽ viết CSS khóa cứng kích thước trang web thay vì làm giao diện responsive.
   - Giải pháp: Với node gốc (`!parent`), mọi trục có giá trị `"fixed"` được đổi thành **`"contextual"`**, đồng thời kích thước thiết kế được chuyển sang trường gợi ý `designedWidth: "1440px"` và `designedHeight: "900px"`.
3. **Chỉ phát `dimensions` (`width`, `height`) khi Thực sự Cố định (`layout.ts:152-179`)**:
   - Đối với các node con, hệ thống xác định hệ trục của cha (`resolveChildAxis`) và kiểm tra xem node có đang bị kéo giãn (`getChildStretch`) hay tự co giãn theo nội dung (`HUG` / `FILL`) không.
   - `dimensions.width` chỉ được ghi ra nếu chiều ngang không bị stretch và `shouldEmitFixedDimension` trả về `true` (tương tự cho `dimensions.height`). Nhờ đó LLM không bao giờ gắn nhầm `width: 200px` cứng vào một nút bấm đang để chế độ `HUG` hay `FILL`.
4. **Vị trí Tuyệt đối (`position: "absolute"`) và `locationRelativeToParent` (`layout.ts:130-140`)**:
   - Khi một node không nằm trong luồng Auto Layout (`parent` là `mode: "none"` hoặc bản thân node có `layoutPositioning === "ABSOLUTE"`):
     - Nếu `n.layoutPositioning === "ABSOLUTE"`, gán `position: "absolute"`.
     - Tính tọa độ tương đối chính xác (làm tròn 2 chữ số thập phân qua `pixelRound`) từ `absoluteBoundingBox` của node trừ đi `absoluteBoundingBox` của `parent`:
       $$x = \text{pixelRound}(n.\text{absoluteBoundingBox}.x - \text{parent}.\text{absoluteBoundingBox}.x)$$
       $$y = \text{pixelRound}(n.\text{absoluteBoundingBox}.y - \text{parent}.\text{absoluteBoundingBox}.y)$$

---

## 2. Style Transformers: Màu sắc, Alpha Compositing & Gradient Toán học (`src/transformers/style/`)

### 2.1. Chuyển đổi Màu & Làm phẳng Chồng Màu Đặc (`src/transformers/style/color.ts`)

1. **Định dạng Hex vs RGBA**:
   - Với mỗi màu `RGBA` (`r, g, b` $\in [0, 1]$) và độ mờ của paint layer `opacity` $\in [0, 1]$:
     $$R = \text{round}(r \times 255), \quad G = \text{round}(g \times 255), \quad B = \text{round}(b \times 255), \quad A = \frac{\text{round}(\text{opacity} \times a \times 100)}{100}$$
   - Nếu $A = 1$, xuất chuỗi Hex 6 ký tự viết hoa `#RRGGBB`. Nếu $A < 1$, xuất chuỗi `rgba(R, G, B, A)`.
2. **Thuật toán Làm phẳng Chồng Màu Đặc (`flattenSolidFills`, dòng 92–111)**:
   - Trong Figma, các Designer thường xếp chồng nhiều lớp màu `SOLID` lên cùng một node (ví dụ: nền trắng `#FFFFFF` ở dưới, phủ một lớp đen mờ `rgba(0, 0, 0, 0.05)` lên trên để làm trạng thái hover, hoặc một lớp màu bị lớp màu đặc 100% phía trên che khuất hoàn toàn).
   - Nếu xuất cả mảng 2–3 màu cho LLM, LLM thường chỉ lấy màu đầu tiên hoặc không biết cách viết CSS chồng nhiều màu nền cho một thẻ `div`.
   - Khi **tất cả** các `fills` hiển thị trên node đều là `SOLID` với `blendMode` mặc định (`NORMAL` hoặc `PASS_THROUGH`), hàm `flattenSolidFills` trộn tuần tự từ đáy lên đỉnh bằng công thức **Source-Over Alpha Compositing** (`blendOver`):
     $$a_{\text{out}} = a_{\text{top}} + a_{\text{bottom}} \cdot (1 - a_{\text{top}})$$
     $$C_{\text{out}} = \frac{C_{\text{top}} \cdot a_{\text{top}} + C_{\text{bottom}} \cdot a_{\text{bottom}} \cdot (1 - a_{\text{top}})}{a_{\text{out}}} \quad \forall C \in \{r, g, b\}$$
   - Kết quả trả về **một mã màu duy nhất** khớp 100% với màu điểm ảnh thực tế hiển thị trên màn hình!
   - Nếu trong mảng `fills` có chứa Gradient, Image, Pattern hoặc Blend Mode đặc biệt (như `MULTIPLY`, `OVERLAY`), `flattenSolidFills` trả về `null` và hệ thống giữ nguyên mảng các lớp sơn nhưng đảo ngược thứ tự (`.reverse()`) để khớp với quy ước CSS (lớp đầu tiên trong CSS `background` nằm trên cùng, ngược với Figma nơi phần tử cuối mảng nằm trên cùng).

---

### 2.2. Công thức Toán học Chuyển đổi Gradient sang CSS (`src/transformers/style/gradient.ts`)

Figma biểu diễn Gradient thông qua mảng điểm điều khiển `gradientHandlePositions` (tọa độ chuẩn hóa $[0, 1] \times [0, 1]$ theo khung bao của node) và mảng `gradientStops` (`position` $\in [0, 1]$ dọc theo đoạn thẳng nối giữa các handle).

#### A. `GRADIENT_LINEAR` $\rightarrow$ `linear-gradient(<angle>deg, ...)` (`mapLinearGradient`, dòng 43–159)
Cho điểm đầu $P_0 = (x_0, y_0) = \text{handles}[0]$ và điểm cuối $P_1 = (x_1, y_1) = \text{handles}[1]$. Đặt $\Delta x = x_1 - x_0$ và $\Delta y = y_1 - y_0$:
1. **Tính góc CSS (`angle`)**:
   Trong hệ tọa độ màn hình (trục $X$ sang phải, trục $Y$ hướng xuống), góc `0deg` của CSS `linear-gradient` hướng thẳng từ dưới lên trên ($-Y$) và tăng theo chiều kim đồng hồ:
   $$\text{angle} = \text{round}\left(\text{atan2}(\Delta y, \Delta x) \times \frac{180}{\pi} + 90\right)$$
2. **Quy chiếu Vị trí Điểm dừng (`findExtendedLineIntersections`)**:
   - Trong Figma, $P_0$ (`position = 0`) và $P_1$ (`position = 1`) có thể được kéo nằm lọt thỏm ở giữa hình chữ nhật (ví dụ từ $x = 0.25$ đến $x = 0.75$). Trong khi đó, `0%` và `100%` của CSS `linear-gradient` luôn nằm ở hai biên ngoài cùng của hộp phần tử!
   - Hàm `findExtendedLineIntersections` tham số hóa đường thẳng $P(t) = P_0 + t \cdot (\Delta x, \Delta y)$ và giải phương trình tìm tham số $t$ tại giao điểm của đường thẳng với 4 cạnh của hình vuông đơn vị ($x = 0, x = 1, y = 0, y = 1$).
   - Gọi $t_{\min}$ và $t_{\max}$ là tham số của 2 giao điểm biên. Mọi vị trí `stop.position` ($p \in [0, 1]$) của Figma được ánh xạ tuyến tính sang phần trăm CSS theo công thức:
     $$p_{\text{css}} = \text{clamp}_{[0, 1]}\left(\frac{p - t_{\min}}{t_{\max} - t_{\min}}\right) \times 100\%$$

#### B. Các loại Gradient khác:
- **`GRADIENT_RADIAL`** (`mapRadialGradient`): Lấy tâm $C = \text{handles}[0]$, xuất `radial-gradient(circle at ${round(Cx*100)}% ${round(Cy*100)}%, ...)`.
- **`GRADIENT_ANGULAR`** (`mapAngularGradient`): Lấy tâm $C = \text{handles}[0]$ và điểm hướng $H = \text{handles}[1]$, tính góc $\theta = \text{round}(\text{atan2}(H_y - C_y, H_x - C_x) \cdot \frac{180}{\pi} + 90)$, xuất `conic-gradient(from ${\theta}deg at ${cx}% ${cy}%, ...)`.
- **`GRADIENT_DIAMOND`** (`mapDiamondGradient`): Xấp xỉ bằng `radial-gradient(ellipse at ${cx}% ${cy}%, ...)`.

---

### 2.3. Strokes & Lý do Lược bỏ `strokeAlign: "INSIDE"` (`src/transformers/style.ts`)
- Hỗ trợ cả độ dày viền đồng nhất (`strokeWeight: "2px"`) lẫn độ dày 4 cạnh riêng biệt (`individualStrokeWeights` được rút gọn qua `generateCSSShorthand` thành `strokeWeights: "0px 0px 1px 0px"`).
- **Lược bỏ `strokeAlign: "INSIDE"` (`src/transformers/style.ts:67-69`)**:
  - Trong Figma, `"INSIDE"` là kiểu viền mặc định trên hầu hết các Frame và hình chữ nhật. Khi lập trình viên hoặc LLM viết CSS hiện đại với `box-sizing: border-box` và `border: 1px solid ...`, viền của CSS tự nhiên nằm bên trong kích thước hộp (tương đương `"INSIDE"`).
  - Do đó, hệ thống **bỏ qua hoàn toàn `"INSIDE"`** và chỉ phát trường `strokeAlign` khi giá trị là `"OUTSIDE"` hoặc `"CENTER"` (khi cần dùng `outline` hoặc `box-shadow` để mô phỏng).

---

## 3. Text & Rich Text Transformer (`src/transformers/text.ts`)

### 3.1. Base Typography (`extractTextStyle`)
- **`lineHeight` (`formatLineHeight`)**:
  - Nếu `lineHeightUnit === "INTRINSIC_%"`: trả về **`undefined`** (vì đây là chế độ `Auto` của Figma, tương ứng với `line-height: normal` mặc định của font).
  - Nếu `lineHeightUnit === "PIXELS"`: trả về `"${pixelRound(lineHeightPx)}px"`.
  - Nếu `lineHeightUnit === "FONT_SIZE_%"`: trả về `"${pixelRound(lineHeightPercentFontSize / 100)}em"`.
- **`letterSpacing`**: Quy đổi từ pixel sang đơn vị tương đối `em` với độ chính xác **4 chữ số thập phân** (`Number((letterSpacing / fontSize).toFixed(4)) + "em"`).

---

### 3.2. Bộ Kết xuất Rich Text Lai: Markdown + Thẻ `{tsN}` (`buildFormattedText`)

Trong Figma, một node `TEXT` duy nhất có thể chứa nhiều đoạn chữ có kiểu dáng khác nhau (ví dụ: một từ được tô đậm, một cụm từ đổi sang màu đỏ `#FF0000` và cỡ chữ lớn hơn, kèm danh sách gạch đầu dòng). Figma lưu thông tin này trong mảng chỉ số `characterStyleOverrides: number[]` (theo từng ký tự Unicode) và bảng `styleOverrideTable: Record<string, TypeStyle>`.

Hàm [`buildFormattedText(node, registerStyle)`](file:///tmp/Figma-Context-MCP/src/transformers/text.ts#L244-L310) giải quyết bài toán này qua quy trình 6 bước:

```mermaid
flowchart TD
    A["`node.characters` + `characterStyleOverrides` + `styleOverrideTable`"] --> B["1. `splitLines`: Tách theo Unicode code points (`Array.from`)\ntại `\\n` hoặc `\\u2029` (không làm vỡ Emoji surrogate pairs)"]
    B --> C["2. `computeRunsForLine` & `computeDelta`:\nGom các ký tự liên tiếp có cùng override ID,\nloại bỏ các thuộc tính trùng với `baseStyle` (no-op overrides),\nhợp nhất các run liền kề có cùng delta"]
    C --> D["3. `detectBoldWeight`:\nTìm mức `fontWeight > baseWeight` xuất hiện trên nhiều ký tự nhất\nđể làm `boldWeight` chuẩn cho ký hiệu `**...**`"]
    D --> E["4. `classifyRun`: Phân tách Delta của mỗi Run thành:\n• Cờ Markdown (`isBold`, `isItalic`, `isStrike`, `urlLink`)\n• Đối tượng `refDelta` (màu `fills`, `fontSize`, `fontFamily`...)"]
    E --> F["5. `renderRun` & `ListState`:\n• Đăng ký `refDelta` lấy mã `ts1`, `ts2`...\n• Bọc từ ngoài vào trong: `{tsN}` → `[](url)` → `~~` → `**` → `*`\n• Thêm tiền tố danh sách (`1. `, `- `) theo `lineTypes` & `lineIndentations`"]
    F --> G["6. Nối các dòng bằng chuỗi literal `'\\\\n'`\n(Giữ toàn bộ văn bản trên 1 dòng đơn khi xuất YAML/Tree)"]
```

- **Ví dụ thực tế đầu ra của `buildFormattedText`**:
  Giả sử node `TEXT` có chữ `"Click here to save 50% today"`, trong đó `"here"` là link gắn nghiêng, còn `"save 50%"` được tô đậm (`fontWeight: 700`) và đổi màu cam `#FF5500`:
  - `node.text`: `"Click [*here*](https://example.com) to {ts1}**save 50%**{/ts1} today"`
  - `node.boldWeight`: `700`
  - `globalVars.styles.ts1`: `{ "fills": ["#FF5500"] }`

---

## 4. Effects & Component Transformers (`src/transformers/effects.ts`, `component.ts`)

### 4.1. Effects Transformer (`buildSimplifiedEffects`)
1. **Đổ bóng (`DROP_SHADOW` & `INNER_SHADOW`)**:
   - Chuyển đổi thành cú pháp CSS: `${offset.x}px ${offset.y}px ${radius}px ${spread ?? 0}px ${formatRGBAColor(color)}` (thêm tiền tố `inset ` cho `INNER_SHADOW`).
   - **Phân biệt theo loại Node**:
     - Nếu `n.type === "TEXT"` $\rightarrow$ gán vào **`textShadow`** (vì trên thẻ văn bản, shadow trong Figma là bóng của chữ chứ không phải bóng của khung hộp).
     - Mọi loại node khác $\rightarrow$ gán vào **`boxShadow`**.
2. **Hiệu ứng Làm mờ (`LAYER_BLUR` & `BACKGROUND_BLUR`)**:
   - **Chia đôi bán kính Blur (`pixelRound(e.radius / 2)`)**: Trong công cụ kết xuất của Figma, giá trị `radius` của Blur bằng $2 \times$ độ lệch chuẩn $\sigma$ của bộ lọc Gaussian, tương ứng với `blur(radius / 2)` trong CSS (khớp chính xác với công thức xuất code của Figma Dev Mode).
   - `LAYER_BLUR` $\rightarrow$ `filter: "blur(${r/2}px)"`.
   - `BACKGROUND_BLUR` $\rightarrow$ `backdropFilter: "blur(${r/2}px)"`.

### 4.2. Component Transformer (`src/transformers/component.ts`)
1. **Làm sạch hậu tố `#nodeId` (`stripPropertyNameSuffix`)**:
   - Figma đặt tên các thuộc tính component kèm mã nội bộ phía sau dấu `#`, ví dụ `"Show Icon#341:0"`. Hàm tự động cắt bỏ phần `#...` để trả về tên thuộc tính sạch `"Show Icon"`.
2. **Trên Node `INSTANCE` (`simplifyComponentProperties`)**:
   - Giữ lại `componentId` và rút gọn bảng `componentProperties` (lọc các thuộc tính `BOOLEAN` và `TEXT`) thành bản đồ phẳng `Record<cleanName, boolean | string>`.
3. **Liên kết Thuộc tính (`simplifyPropertyReferences`)**:
   - Cho biết thuộc tính nào của một node con bên trong Component được điều khiển bởi prop nào: giữ lại khóa `"visible"` và đổi tên khóa `"characters"` của Figma thành **`"text"`** (để đồng nhất với trường `text` trên `SimplifiedNode`).
