package transformers

import (
	"fmt"
	"sort"
	"strings"
)

// SimplifiedLayout holds Flexbox, Grid, Sizing, and Positioning properties.
type SimplifiedLayout struct {
	Sizing                   map[string]string  `json:"sizing,omitempty" yaml:"sizing,omitempty"`
	Dimensions               map[string]float64 `json:"dimensions,omitempty" yaml:"dimensions,omitempty"`
	LocationRelativeToParent map[string]float64 `json:"locationRelativeToParent,omitempty" yaml:"locationRelativeToParent,omitempty"`
	ZIndex                   *int               `json:"zIndex,omitempty" yaml:"zIndex,omitempty"`
	Mode                     string             `json:"mode" yaml:"mode"`
	JustifyContent           string             `json:"justifyContent,omitempty" yaml:"justifyContent,omitempty"`
	AlignItems               string             `json:"alignItems,omitempty" yaml:"alignItems,omitempty"`
	AlignSelf                string             `json:"alignSelf,omitempty" yaml:"alignSelf,omitempty"`
	JustifySelf              string             `json:"justifySelf,omitempty" yaml:"justifySelf,omitempty"`
	Gap                      string             `json:"gap,omitempty" yaml:"gap,omitempty"`
	Padding                  string             `json:"padding,omitempty" yaml:"padding,omitempty"`
	Position                 string             `json:"position,omitempty" yaml:"position,omitempty"`
	GridTemplateColumns      string             `json:"gridTemplateColumns,omitempty" yaml:"gridTemplateColumns,omitempty"`
	GridTemplateRows         string             `json:"gridTemplateRows,omitempty" yaml:"gridTemplateRows,omitempty"`
	GridColumn               string             `json:"gridColumn,omitempty" yaml:"gridColumn,omitempty"`
	GridRow                  string             `json:"gridRow,omitempty" yaml:"gridRow,omitempty"`
	DesignedWidth            string             `json:"designedWidth,omitempty" yaml:"designedWidth,omitempty"`
	DesignedHeight           string             `json:"designedHeight,omitempty" yaml:"designedHeight,omitempty"`
	OverflowScroll           []string           `json:"overflowScroll,omitempty" yaml:"overflowScroll,omitempty"`
	Wrap                     bool               `json:"wrap,omitempty" yaml:"wrap,omitempty"`
	ClipsContent             bool               `json:"clipsContent,omitempty" yaml:"clipsContent,omitempty"`
}

// ToMap converts SimplifiedLayout to a map[string]any, omitting empty fields.
// Returns nil if only mode=="none" is present.
func (l SimplifiedLayout) ToMap() map[string]any {
	m := map[string]any{
		"mode": l.Mode,
	}
	if l.JustifyContent != "" {
		m["justifyContent"] = l.JustifyContent
	}
	if l.AlignItems != "" {
		m["alignItems"] = l.AlignItems
	}
	if l.AlignSelf != "" {
		m["alignSelf"] = l.AlignSelf
	}
	if l.JustifySelf != "" {
		m["justifySelf"] = l.JustifySelf
	}
	if l.Wrap {
		m["wrap"] = true
	}
	if l.Gap != "" {
		m["gap"] = l.Gap
	}
	if l.Padding != "" {
		m["padding"] = l.Padding
	}
	if l.Position != "" {
		m["position"] = l.Position
	}
	if len(l.LocationRelativeToParent) > 0 {
		m["locationRelativeToParent"] = l.LocationRelativeToParent
	}
	if len(l.Dimensions) > 0 {
		m["dimensions"] = l.Dimensions
	}
	if len(l.Sizing) > 0 {
		m["sizing"] = l.Sizing
	}
	if l.DesignedWidth != "" {
		m["designedWidth"] = l.DesignedWidth
	}
	if l.DesignedHeight != "" {
		m["designedHeight"] = l.DesignedHeight
	}
	if len(l.OverflowScroll) > 0 {
		m["overflowScroll"] = l.OverflowScroll
	}
	if l.ClipsContent {
		m["clipsContent"] = true
	}
	if l.GridTemplateColumns != "" {
		m["gridTemplateColumns"] = l.GridTemplateColumns
	}
	if l.GridTemplateRows != "" {
		m["gridTemplateRows"] = l.GridTemplateRows
	}
	if l.GridColumn != "" {
		m["gridColumn"] = l.GridColumn
	}
	if l.GridRow != "" {
		m["gridRow"] = l.GridRow
	}
	if l.ZIndex != nil {
		m["zIndex"] = *l.ZIndex
	}
	if len(m) <= 1 && l.Mode == "none" {
		return nil
	}
	return m
}

// IsFrame checks if a node has frame-like behavior (clipsContent boolean or FRAME/GROUP/COMPONENT/INSTANCE types).
func IsFrame(n map[string]any) bool {
	if n == nil {
		return false
	}
	if _, ok := n["clipsContent"].(bool); ok {
		return true
	}
	switch getString(n, "type") {
	case "FRAME", "GROUP", "COMPONENT", "COMPONENT_SET", "INSTANCE":
		return true
	default:
		return false
	}
}

// LayoutModeToSchema converts Figma layoutMode to "row", "column", "grid", or "none".
func LayoutModeToSchema(n map[string]any) string {
	if !IsFrame(n) {
		return "none"
	}
	switch getString(n, "layoutMode") {
	case "HORIZONTAL":
		return "row"
	case "VERTICAL":
		return "column"
	case "GRID":
		return "grid"
	default:
		return "none"
	}
}

// IsInAutoLayoutFlow returns true when parent uses Auto Layout (row/column/grid) and child is not ABSOLUTE.
func IsInAutoLayoutFlow(node, parent map[string]any) bool {
	if parent == nil {
		return false
	}
	pMode := LayoutModeToSchema(parent)
	if pMode == "none" {
		return false
	}
	return getString(node, "layoutPositioning") != "ABSOLUTE"
}

// BuildSimplifiedLayout computes the complete SimplifiedLayout for node n within parent.
func BuildSimplifiedLayout(n, parent map[string]any) SimplifiedLayout {
	mode := LayoutModeToSchema(n)
	out := SimplifiedLayout{Mode: mode}

	// 1. Frame container properties
	if IsFrame(n) {
		children := filterVisibleChildren(getMapSlice(n, "children"))
		switch mode {
		case "row", "column":
			out.JustifyContent = convertJustifyContent(getString(n, "primaryAxisAlignItems"))
			out.AlignItems = convertAlignItems(getString(n, "counterAxisAlignItems"), children, mode)
			if getString(n, "layoutWrap") == "WRAP" {
				out.Wrap = true
			}
			out.Gap = buildFlexGap(n, mode)
		case "grid":
			out.GridTemplateColumns = strings.TrimSpace(getString(n, "gridColumnsSizing"))
			out.GridTemplateRows = strings.TrimSpace(getString(n, "gridRowsSizing"))
			rGap, _ := getFloat(n, "gridRowGap")
			cGap, _ := getFloat(n, "gridColumnGap")
			out.Gap = gapShorthand(rGap, cGap)
		}

		pTop, _ := getFloat(n, "paddingTop")
		pRight, _ := getFloat(n, "paddingRight")
		pBottom, _ := getFloat(n, "paddingBottom")
		pLeft, _ := getFloat(n, "paddingLeft")
		out.Padding = GenerateCSSShorthand(pTop, pRight, pBottom, pLeft, true)

		overflowDir := getString(n, "overflowDirection")
		if strings.Contains(overflowDir, "HORIZONTAL") {
			out.OverflowScroll = append(out.OverflowScroll, "x")
		}
		if strings.Contains(overflowDir, "VERTICAL") {
			out.OverflowScroll = append(out.OverflowScroll, "y")
		}
		if getBool(n, "clipsContent", false) && mode != "none" {
			// Only emit clipsContent when explicitly needed or when overflow is clipped
		}
	}

	// 2. Sizing & Dimensions within parent
	hSizing := convertSizing(getString(n, "layoutSizingHorizontal"))
	vSizing := convertSizing(getString(n, "layoutSizingVertical"))
	bbox, hasBBox := extractBBox(n)

	isRoot := parent == nil
	if isRoot {
		if hSizing == "fixed" {
			hSizing = "contextual"
		}
		if vSizing == "fixed" {
			vSizing = "contextual"
		}
		if hasBBox {
			if hSizing == "contextual" && bbox.W > 0 {
				out.DesignedWidth = FormatPx(bbox.W)
			}
			if vSizing == "contextual" && bbox.H > 0 {
				out.DesignedHeight = FormatPx(bbox.H)
			}
		}
	}

	if hSizing != "" || vSizing != "" {
		out.Sizing = make(map[string]string)
		if hSizing != "" {
			out.Sizing["horizontal"] = hSizing
		}
		if vSizing != "" {
			out.Sizing["vertical"] = vSizing
		}
	}

	// Alignment & Grid child positioning
	parentMode := LayoutModeToSchema(parent)
	if IsInAutoLayoutFlow(n, parent) {
		if parentMode == "grid" {
			parentChildren := filterVisibleChildren(getMapSlice(parent, "children"))
			packed := IsPackedGrid(parentChildren)
			buildGridChildPositioning(n, parentChildren, packed, &out)
		} else {
			out.AlignSelf = convertSelfAlign(getString(n, "layoutAlign"))
		}
	}

	// Relative location when not in auto-layout flow
	if parent != nil && !IsInAutoLayoutFlow(n, parent) {
		if getString(n, "layoutPositioning") == "ABSOLUTE" {
			out.Position = "absolute"
		}
		if parentBBox, ok := extractBBox(parent); ok && hasBBox {
			out.LocationRelativeToParent = map[string]float64{
				"x": PixelRound(bbox.X - parentBBox.X),
				"y": PixelRound(bbox.Y - parentBBox.Y),
			}
		}
	}

	// Fixed dimensions for non-root nodes
	if !isRoot && hasBBox {
		axis := resolveChildAxis(n, parent, mode, parentMode == "grid")
		stretchH, stretchV := getChildStretch(n, axis)
		rawHSizing := getString(n, "layoutSizingHorizontal")
		rawVSizing := getString(n, "layoutSizingVertical")

		dims := make(map[string]float64)
		if !stretchH && shouldEmitFixedDimension(rawHSizing, axis) && bbox.W > 0 {
			dims["width"] = PixelRound(bbox.W)
		}
		if !stretchV && shouldEmitFixedDimension(rawVSizing, axis) && bbox.H > 0 {
			dims["height"] = PixelRound(bbox.H)
		}
		if axis == "column" && getBool(n, "preserveRatio", false) && bbox.H > 0 {
			dims["aspectRatio"] = PixelRound(bbox.W / bbox.H)
		}
		if len(dims) > 0 {
			out.Dimensions = dims
		}
	}

	return out
}

type rectBBox struct {
	X, Y, W, H float64
}

func extractBBox(n map[string]any) (rectBBox, bool) {
	if n == nil {
		return rectBBox{}, false
	}
	b, ok := n["absoluteBoundingBox"].(map[string]any)
	if !ok {
		return rectBBox{}, false
	}
	x, _ := getFloat(b, "x")
	y, _ := getFloat(b, "y")
	w, okW := getFloat(b, "width")
	h, okH := getFloat(b, "height")
	if !okW && !okH {
		return rectBBox{}, false
	}
	return rectBBox{X: x, Y: y, W: w, H: h}, true
}

func filterVisibleChildren(children []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(children))
	for _, c := range children {
		if isVisibleMap(c) {
			out = append(out, c)
		}
	}
	return out
}

func convertJustifyContent(align string) string {
	switch align {
	case "MAX":
		return "flex-end"
	case "CENTER":
		return "center"
	case "SPACE_BETWEEN":
		return "space-between"
	default:
		return ""
	}
}

func convertAlignItems(align string, children []map[string]any, mode string) string {
	if len(children) > 0 {
		crossKey := "layoutSizingVertical"
		if mode == "column" {
			crossKey = "layoutSizingHorizontal"
		}
		allStretch := true
		hasInFlow := false
		for _, ch := range children {
			if getString(ch, "layoutPositioning") == "ABSOLUTE" {
				continue
			}
			hasInFlow = true
			if getString(ch, crossKey) != "FILL" {
				allStretch = false
				break
			}
		}
		if hasInFlow && allStretch {
			return "stretch"
		}
	}
	switch align {
	case "MAX":
		return "flex-end"
	case "CENTER":
		return "center"
	case "BASELINE":
		return "baseline"
	default:
		return ""
	}
}

func convertSelfAlign(align string) string {
	switch align {
	case "MAX":
		return "flex-end"
	case "CENTER":
		return "center"
	case "STRETCH":
		return "stretch"
	default:
		return ""
	}
}

func convertSizing(s string) string {
	switch s {
	case "FIXED":
		return "fixed"
	case "FILL":
		return "fill"
	case "HUG":
		return "hug"
	default:
		return ""
	}
}

func buildFlexGap(n map[string]any, mode string) string {
	var primaryGap float64
	if getString(n, "primaryAxisAlignItems") != "SPACE_BETWEEN" {
		primaryGap, _ = getFloat(n, "itemSpacing")
	}
	var counterGap float64
	if getString(n, "layoutWrap") == "WRAP" && getString(n, "counterAxisAlignContent") != "SPACE_BETWEEN" {
		counterGap, _ = getFloat(n, "counterAxisSpacing")
	}
	if mode == "row" {
		return gapShorthand(counterGap, primaryGap)
	}
	return gapShorthand(primaryGap, counterGap)
}

func gapShorthand(rowGap, colGap float64) string {
	r := PixelRound(rowGap)
	c := PixelRound(colGap)
	if r <= 0 && c <= 0 {
		return ""
	}
	if r > 0 && c > 0 {
		if r == c {
			return fmt.Sprintf("%gpx", r)
		}
		return fmt.Sprintf("%gpx %gpx", r, c)
	}
	if r > 0 {
		return fmt.Sprintf("%gpx", r)
	}
	return fmt.Sprintf("%gpx", c)
}

func resolveChildAxis(n, parent map[string]any, selfMode string, parentIsGrid bool) string {
	if parentIsGrid {
		return "grid"
	}
	if IsInAutoLayoutFlow(n, parent) {
		return LayoutModeToSchema(parent)
	}
	return selfMode
}

func getChildStretch(n map[string]any, axis string) (horizontal, vertical bool) {
	switch axis {
	case "grid":
		return getString(n, "layoutSizingHorizontal") == "FILL", getString(n, "layoutSizingVertical") == "FILL"
	case "row":
		grow, _ := getFloat(n, "layoutGrow")
		return grow > 0 || getString(n, "layoutSizingHorizontal") == "FILL", getString(n, "layoutAlign") == "STRETCH" || getString(n, "layoutSizingVertical") == "FILL"
	case "column":
		grow, _ := getFloat(n, "layoutGrow")
		return getString(n, "layoutAlign") == "STRETCH" || getString(n, "layoutSizingHorizontal") == "FILL", grow > 0 || getString(n, "layoutSizingVertical") == "FILL"
	default:
		return false, false
	}
}

func shouldEmitFixedDimension(sizing, axis string) bool {
	if axis == "row" || axis == "column" {
		return sizing == "FIXED"
	}
	return sizing == "" || sizing == "FIXED"
}

// ComputeGridChildOrder reorders in-flow grid children by (gridRowAnchorIndex, gridColumnAnchorIndex)
// while keeping ABSOLUTE children at their relative positions.
func ComputeGridChildOrder(parent map[string]any) []map[string]any {
	children := getMapSlice(parent, "children")
	if LayoutModeToSchema(parent) != "grid" || len(children) <= 1 {
		return children
	}

	type indexedChild struct {
		node     map[string]any
		origIdx  int
		row      int
		col      int
		isInFlow bool
	}

	items := make([]indexedChild, len(children))
	var inFlow []indexedChild
	for i, ch := range children {
		abs := getString(ch, "layoutPositioning") == "ABSOLUTE"
		r, _ := getFloat(ch, "gridRowAnchorIndex")
		c, _ := getFloat(ch, "gridColumnAnchorIndex")
		ic := indexedChild{
			node:     ch,
			origIdx:  i,
			row:      int(r),
			col:      int(c),
			isInFlow: !abs,
		}
		items[i] = ic
		if !abs {
			inFlow = append(inFlow, ic)
		}
	}

	sort.SliceStable(inFlow, func(i, j int) bool {
		if inFlow[i].row != inFlow[j].row {
			return inFlow[i].row < inFlow[j].row
		}
		return inFlow[i].col < inFlow[j].col
	})

	out := make([]map[string]any, len(children))
	flowPos := 0
	for i, it := range items {
		if !it.isInFlow {
			out[i] = it.node
		} else {
			out[i] = inFlow[flowPos].node
			flowPos++
		}
	}
	return out
}

// IsPackedGrid returns true if in-flow grid children completely fill their bounding grid without gaps or overlaps.
func IsPackedGrid(children []map[string]any) bool {
	occupied := make(map[string]bool)
	maxRow, maxCol := -1, -1
	inFlowCount := 0

	for _, ch := range children {
		if getString(ch, "layoutPositioning") == "ABSOLUTE" {
			continue
		}
		inFlowCount++
		r0f, _ := getFloat(ch, "gridRowAnchorIndex")
		c0f, _ := getFloat(ch, "gridColumnAnchorIndex")
		rSpanF, okRS := getFloat(ch, "gridRowSpan")
		if !okRS || rSpanF < 1 {
			rSpanF = 1
		}
		cSpanF, okCS := getFloat(ch, "gridColumnSpan")
		if !okCS || cSpanF < 1 {
			cSpanF = 1
		}
		r0, c0 := int(r0f), int(c0f)
		rSpan, cSpan := int(rSpanF), int(cSpanF)

		for r := r0; r < r0+rSpan; r++ {
			for c := c0; c < c0+cSpan; c++ {
				key := fmt.Sprintf("%d:%d", r, c)
				if occupied[key] {
					return false // overlap
				}
				occupied[key] = true
				if r > maxRow {
					maxRow = r
				}
				if c > maxCol {
					maxCol = c
				}
			}
		}
	}

	if inFlowCount == 0 {
		return true
	}
	return len(occupied) == (maxRow+1)*(maxCol+1)
}

func buildGridChildPositioning(n map[string]any, siblings []map[string]any, packed bool, out *SimplifiedLayout) {
	colAnchorF, _ := getFloat(n, "gridColumnAnchorIndex")
	rowAnchorF, _ := getFloat(n, "gridRowAnchorIndex")
	colSpanF, okCS := getFloat(n, "gridColumnSpan")
	if !okCS || colSpanF < 1 {
		colSpanF = 1
	}
	rowSpanF, okRS := getFloat(n, "gridRowSpan")
	if !okRS || rowSpanF < 1 {
		rowSpanF = 1
	}

	col := int(colAnchorF) + 1
	row := int(rowAnchorF) + 1
	colSpan := int(colSpanF)
	rowSpan := int(rowSpanF)

	if packed {
		if colSpan > 1 {
			out.GridColumn = fmt.Sprintf("span %d", colSpan)
		}
		if rowSpan > 1 {
			out.GridRow = fmt.Sprintf("span %d", rowSpan)
		}
	} else {
		if colSpan > 1 {
			out.GridColumn = fmt.Sprintf("%d / span %d", col, colSpan)
		} else {
			out.GridColumn = fmt.Sprintf("%d", col)
		}
		if rowSpan > 1 {
			out.GridRow = fmt.Sprintf("%d / span %d", row, rowSpan)
		} else {
			out.GridRow = fmt.Sprintf("%d", row)
		}
	}

	switch getString(n, "gridChildHorizontalAlign") {
	case "MIN":
		out.JustifySelf = "start"
	case "CENTER":
		out.JustifySelf = "center"
	case "MAX":
		out.JustifySelf = "end"
	}
	switch getString(n, "gridChildVerticalAlign") {
	case "MIN":
		out.AlignSelf = "start"
	case "CENTER":
		out.AlignSelf = "center"
	case "MAX":
		out.AlignSelf = "end"
	}

	// Check if zIndex is required due to overlapping AABBs when reordered
	if gridChildrenOverlap(siblings) {
		id := getString(n, "id")
		for idx, sib := range siblings {
			if getString(sib, "id") == id {
				z := idx
				out.ZIndex = &z
				break
			}
		}
	}
}

func gridChildrenOverlap(siblings []map[string]any) bool {
	var boxes []rectBBox
	for _, s := range siblings {
		if getString(s, "layoutPositioning") == "ABSOLUTE" {
			continue
		}
		if b, ok := extractBBox(s); ok {
			boxes = append(boxes, b)
		}
	}
	for i := 0; i < len(boxes); i++ {
		for j := i + 1; j < len(boxes); j++ {
			a, b := boxes[i], boxes[j]
			if a.X < b.X+b.W && a.X+a.W > b.X && a.Y < b.Y+b.H && a.Y+a.H > b.Y {
				return true
			}
		}
	}
	return false
}
