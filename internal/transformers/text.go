package transformers

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var markdownEscapeRegex = regexp.MustCompile(`([\\*_~\[\](){}])`)

// ExtractTextStyle extracts the base typography properties from a TEXT node.
func ExtractTextStyle(n map[string]any) map[string]any {
	style, ok := n["style"].(map[string]any)
	if !ok || len(style) == 0 {
		return nil
	}
	out := make(map[string]any)

	if ff := getString(style, "fontFamily"); ff != "" {
		out["fontFamily"] = ff
	}
	if fs := getString(style, "fontStyle"); fs != "" {
		out["fontStyle"] = fs
	}
	if fw, ok := getFloat(style, "fontWeight"); ok && fw > 0 {
		out["fontWeight"] = int(fw)
	}
	fontSize, hasFontSize := getFloat(style, "fontSize")
	if hasFontSize && fontSize > 0 {
		out["fontSize"] = PixelRound(fontSize)
	}

	if lh := formatLineHeight(style, fontSize); lh != "" {
		out["lineHeight"] = lh
	}
	if ls, ok := getFloat(style, "letterSpacing"); ok && ls != 0 && fontSize > 0 {
		em := EmRound(ls / fontSize)
		if em != 0 {
			out["letterSpacing"] = fmt.Sprintf("%gem", em)
		}
	}
	if tc := getString(style, "textCase"); tc != "" && tc != "ORIGINAL" {
		out["textCase"] = tc
	}
	if tah := getString(style, "textAlignHorizontal"); tah != "" && tah != "LEFT" {
		out["textAlignHorizontal"] = tah
	}
	if tav := getString(style, "textAlignVertical"); tav != "" && tav != "TOP" {
		out["textAlignVertical"] = tav
	}
	if getBool(style, "italic", false) {
		out["italic"] = true
	}
	td := getString(style, "textDecoration")
	if td == "UNDERLINE" || td == "STRIKETHROUGH" {
		out["textDecoration"] = td
	}
	if ps, ok := getFloat(style, "paragraphSpacing"); ok && ps > 0 {
		out["paragraphSpacing"] = PixelRound(ps)
	}
	if pi, ok := getFloat(style, "paragraphIndent"); ok && pi > 0 {
		out["paragraphIndent"] = PixelRound(pi)
	}
	if ls, ok := getFloat(style, "listSpacing"); ok && ls > 0 {
		out["listSpacing"] = PixelRound(ls)
	}
	if hl, ok := style["hyperlink"].(map[string]any); ok && len(hl) > 0 {
		if getString(hl, "type") != "" {
			out["hyperlink"] = hl
		}
	}
	if otf, ok := style["opentypeFlags"].(map[string]any); ok {
		nonZero := make(map[string]any)
		for k, v := range otf {
			if f, ok := v.(float64); ok && f != 0 {
				nonZero[k] = int(f)
			}
		}
		if len(nonZero) > 0 {
			out["opentypeFlags"] = nonZero
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func formatLineHeight(style map[string]any, fontSize float64) string {
	unit := getString(style, "lineHeightUnit")
	switch unit {
	case "INTRINSIC_%":
		return ""
	case "PIXELS":
		if px, ok := getFloat(style, "lineHeightPx"); ok && px > 0 {
			return fmt.Sprintf("%gpx", PixelRound(px))
		}
	case "FONT_SIZE_%":
		if pct, ok := getFloat(style, "lineHeightPercentFontSize"); ok && pct > 0 {
			return fmt.Sprintf("%gem", PixelRound(pct/100.0))
		}
	default:
		if px, ok := getFloat(style, "lineHeightPx"); ok && px > 0 && fontSize > 0 {
			return fmt.Sprintf("%gem", PixelRound(px/fontSize))
		}
	}
	return ""
}

type textRun struct {
	text  string
	delta map[string]any
}

// BuildFormattedText renders a TEXT node's characters + characterStyleOverrides + styleOverrideTable
// into a hybrid Markdown + {tsN}...{/tsN} inline style string, returning (formattedText, boldWeight).
func BuildFormattedText(n map[string]any, registerInline func(delta map[string]any) string) (string, int) {
	chars := getString(n, "characters")
	if chars == "" {
		return "", 0
	}

	baseStyle, _ := n["style"].(map[string]any)
	overrideTable, _ := n["styleOverrideTable"].(map[string]any)
	overrides := extractIntSlice(n["characterStyleOverrides"])
	lineTypes := extractStringSlice(n["lineTypes"])
	lineIndents := extractIntSlice(n["lineIndentations"])

	runes := []rune(chars)
	if len(overrides) == 0 || len(overrideTable) == 0 {
		return formatPlainLines(runes, lineTypes, lineIndents), 0
	}

	// Split into lines by \n or \u2029
	type rawLine struct {
		runes     []rune
		overrides []int
	}
	var lines []rawLine
	curRunes := []rune{}
	curOverrides := []int{}

	for idx, r := range runes {
		ov := 0
		if idx < len(overrides) {
			ov = overrides[idx]
		}
		if r == '\n' || r == '\u2029' {
			lines = append(lines, rawLine{runes: curRunes, overrides: curOverrides})
			curRunes = nil
			curOverrides = nil
		} else {
			curRunes = append(curRunes, r)
			curOverrides = append(curOverrides, ov)
		}
	}
	lines = append(lines, rawLine{runes: curRunes, overrides: curOverrides})

	// Compute runs per line & detect dominant boldWeight
	baseWeight := 400
	if bw, ok := getFloat(baseStyle, "fontWeight"); ok && bw > 0 {
		baseWeight = int(bw)
	}
	baseItalic := getBool(baseStyle, "italic", false)

	lineRuns := make([][]textRun, len(lines))
	weightCharCounts := make(map[int]int)

	for i, ln := range lines {
		runs := computeLineRuns(ln.runes, ln.overrides, overrideTable, baseStyle)
		lineRuns[i] = runs
		for _, rn := range runs {
			if w, ok := rn.delta["fontWeight"].(int); ok && w > baseWeight {
				weightCharCounts[w] += len([]rune(rn.text))
			}
		}
	}

	boldWeight := 0
	bestCount := 0
	for w, cnt := range weightCharCounts {
		if cnt > bestCount || (cnt == bestCount && w > boldWeight) {
			bestCount = cnt
			boldWeight = w
		}
	}

	listCounters := make(map[int]int)
	renderedLines := make([]string, len(lines))

	for i, runs := range lines {
		_ = runs
		var sb strings.Builder
		for _, rn := range lineRuns[i] {
			sb.WriteString(renderSingleRun(rn, baseWeight, boldWeight, baseItalic, registerInline))
		}
		prefix := computeListPrefix(i, lineTypes, lineIndents, listCounters)
		renderedLines[i] = prefix + sb.String()
	}

	return strings.Join(renderedLines, `\n`), boldWeight
}

func formatPlainLines(runes []rune, lineTypes []string, lineIndents []int) string {
	var lines []string
	var cur []rune
	for _, r := range runes {
		if r == '\n' || r == '\u2029' {
			lines = append(lines, string(cur))
			cur = nil
		} else {
			cur = append(cur, r)
		}
	}
	lines = append(lines, string(cur))

	listCounters := make(map[int]int)
	for i := range lines {
		prefix := computeListPrefix(i, lineTypes, lineIndents, listCounters)
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, `\n`)
}

func computeListPrefix(lineIdx int, lineTypes []string, lineIndents []int, counters map[int]int) string {
	if lineIdx >= len(lineTypes) {
		return ""
	}
	lt := lineTypes[lineIdx]
	depth := 0
	if lineIdx < len(lineIndents) && lineIndents[lineIdx] > 0 {
		depth = lineIndents[lineIdx]
	}
	indent := strings.Repeat("  ", depth)
	switch lt {
	case "ORDERED":
		counters[depth]++
		return fmt.Sprintf("%s%d. ", indent, counters[depth])
	case "UNORDERED":
		delete(counters, depth)
		return indent + "- "
	default:
		delete(counters, depth)
		return ""
	}
}

func computeLineRuns(runes []rune, overrides []int, overrideTable, baseStyle map[string]any) []textRun {
	if len(runes) == 0 {
		return nil
	}
	var out []textRun
	start := 0
	curID := overrides[0]

	for i := 1; i <= len(runes); i++ {
		if i == len(runes) || overrides[i] != curID {
			segment := string(runes[start:i])
			delta := computeStyleDelta(curID, overrideTable, baseStyle)
			if len(out) > 0 && mapsEqual(out[len(out)-1].delta, delta) {
				out[len(out)-1].text += segment
			} else {
				out = append(out, textRun{text: segment, delta: delta})
			}
			if i < len(runes) {
				start = i
				curID = overrides[i]
			}
		}
	}
	return out
}

func computeStyleDelta(overrideID int, overrideTable, baseStyle map[string]any) map[string]any {
	if overrideID == 0 || overrideTable == nil {
		return nil
	}
	ov, ok := overrideTable[fmt.Sprintf("%d", overrideID)].(map[string]any)
	if !ok || len(ov) == 0 {
		return nil
	}

	delta := make(map[string]any)
	if ff := getString(ov, "fontFamily"); ff != "" && ff != getString(baseStyle, "fontFamily") {
		delta["fontFamily"] = ff
	}
	if fw, ok := getFloat(ov, "fontWeight"); ok && fw > 0 {
		baseFW, _ := getFloat(baseStyle, "fontWeight")
		if int(fw) != int(baseFW) {
			delta["fontWeight"] = int(fw)
		}
	}
	if fs, ok := getFloat(ov, "fontSize"); ok && fs > 0 {
		baseFS, _ := getFloat(baseStyle, "fontSize")
		if PixelRound(fs) != PixelRound(baseFS) {
			delta["fontSize"] = PixelRound(fs)
		}
	}
	if it, ok := ov["italic"].(bool); ok && it != getBool(baseStyle, "italic", false) {
		delta["italic"] = it
	}
	if td := getString(ov, "textDecoration"); td != "" && td != getString(baseStyle, "textDecoration") {
		delta["textDecoration"] = td
	}
	if tc := getString(ov, "textCase"); tc != "" && tc != getString(baseStyle, "textCase") {
		delta["textCase"] = tc
	}
	if hl, ok := ov["hyperlink"].(map[string]any); ok && len(hl) > 0 {
		delta["hyperlink"] = hl
	}
	if rawFills := getMapSlice(ov, "fills"); len(rawFills) > 0 {
		var vis []map[string]any
		for _, f := range rawFills {
			if isVisibleMap(f) {
				vis = append(vis, f)
			}
		}
		if len(vis) > 0 {
			if flat, ok := FlattenSolidFills(vis); ok {
				delta["fills"] = []any{flat}
			} else {
				var parsed []any
				for _, f := range vis {
					if pf, ok := ParsePaint(f, false); ok {
						parsed = append(parsed, pf.ToSerializable())
					}
				}
				for i, j := 0, len(parsed)-1; i < j; i, j = i+1, j-1 {
					parsed[i], parsed[j] = parsed[j], parsed[i]
				}
				if len(parsed) > 0 {
					delta["fills"] = parsed
				}
			}
		}
	}

	if len(delta) == 0 {
		return nil
	}
	return delta
}

func renderSingleRun(rn textRun, baseWeight, boldWeight int, baseItalic bool, registerInline func(map[string]any) string) string {
	if len(rn.delta) == 0 || strings.TrimSpace(rn.text) == "" {
		return rn.text
	}

	leadWS, core, trailWS := splitEdgeWhitespace(rn.text)
	if core == "" {
		return rn.text
	}

	isBold := false
	isItalic := false
	isStrike := false
	urlLink := ""
	refDelta := make(map[string]any)

	for k, v := range rn.delta {
		switch k {
		case "fontWeight":
			w, _ := v.(int)
			if w > baseWeight {
				isBold = true
				if boldWeight > 0 && w != boldWeight {
					refDelta["fontWeight"] = w
				}
			} else {
				refDelta["fontWeight"] = w
			}
		case "italic":
			it, _ := v.(bool)
			if it && !baseItalic {
				isItalic = true
			} else {
				refDelta["italic"] = it
			}
		case "textDecoration":
			td, _ := v.(string)
			if td == "STRIKETHROUGH" {
				isStrike = true
			} else {
				refDelta["textDecoration"] = td
			}
		case "hyperlink":
			hl, _ := v.(map[string]any)
			if getString(hl, "type") == "URL" && getString(hl, "url") != "" {
				urlLink = getString(hl, "url")
			} else {
				refDelta["hyperlink"] = hl
			}
		default:
			refDelta[k] = v
		}
	}

	escaped := markdownEscapeRegex.ReplaceAllString(core, `\$1`)
	inner := escaped
	if isItalic {
		inner = "*" + inner + "*"
	}
	if isBold {
		inner = "**" + inner + "**"
	}
	if isStrike {
		inner = "~~" + inner + "~~"
	}
	if urlLink != "" {
		inner = fmt.Sprintf("[%s](%s)", inner, urlLink)
	}
	if len(refDelta) > 0 && registerInline != nil {
		tag := registerInline(refDelta)
		if tag != "" {
			inner = fmt.Sprintf("{%s}%s{/%s}", tag, inner, tag)
		}
	}

	return leadWS + inner + trailWS
}

func splitEdgeWhitespace(s string) (lead, core, trail string) {
	trimmedLeft := strings.TrimLeft(s, " \t")
	lead = s[:len(s)-len(trimmedLeft)]
	core = strings.TrimRight(trimmedLeft, " \t")
	trail = trimmedLeft[len(core):]
	return lead, core, trail
}

func extractIntSlice(v any) []int {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]int, len(raw))
	for i, item := range raw {
		switch num := item.(type) {
		case float64:
			out[i] = int(num)
		case int:
			out[i] = num
		}
	}
	return out
}

func extractStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(raw))
	for i, item := range raw {
		if s, ok := item.(string); ok {
			out[i] = s
		}
	}
	return out
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return StableStringify(a) == StableStringify(b)
}

// StableStringify produces a deterministic string representation of an arbitrary map/slice/primitive.
func StableStringify(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", val)
	case bool:
		return fmt.Sprintf("%t", val)
	case int:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%g", val)
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = StableStringify(item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case []string:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = fmt.Sprintf("%q", item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case []float64:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = fmt.Sprintf("%g", item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			if val[k] == nil {
				continue
			}
			parts = append(parts, fmt.Sprintf("%q:%s", k, StableStringify(val[k])))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case map[string]string:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%q:%q", k, val[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case map[string]float64:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%q:%g", k, val[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return fmt.Sprintf("%v", val)
	}
}
