package images

import (
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var (
	fileNameRegex      = regexp.MustCompile(`^[a-zA-Z0-9_.-]+\.(png|svg|gif|jpg|jpeg)$`)
	suffixRegex        = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	driveLetterOnPOSIX = regexp.MustCompile(`^[A-Za-z]:[/\\]`)
	svgTagRegex        = regexp.MustCompile(`(?is)<svg\b[^>]*>`)
	svgWidthRegex      = regexp.MustCompile(`(?i)\bwidth\s*=\s*["']([^"']+)["']`)
	svgHeightRegex     = regexp.MustCompile(`(?i)\bheight\s*=\s*["']([^"']+)["']`)
	svgViewBoxRegex    = regexp.MustCompile(`(?i)\bviewBox\s*=\s*["']([^"']+)["']`)
	splitViewBoxRegex  = regexp.MustCompile(`[\s,]+`)
)

// Dimensions holds pixel width and height of a downloaded or cropped image.
type Dimensions struct {
	Width  int `json:"width" yaml:"width"`
	Height int `json:"height" yaml:"height"`
}

// ValidateFileName verifies that a requested file name has no path separators or traversal segments.
func ValidateFileName(fileName string) error {
	fileName = strings.TrimSpace(fileName)
	if !fileNameRegex.MatchString(fileName) || strings.Contains(fileName, "..") {
		return fmt.Errorf("invalid fileName %q: must contain only letters, numbers, underscores, dots, or hyphens and end with .png, .svg, .gif, .jpg, or .jpeg", fileName)
	}
	return nil
}

// ValidateFilenameSuffix verifies that an optional crop suffix is alphanumeric/hyphen/underscore.
func ValidateFilenameSuffix(suffix string) error {
	if suffix == "" {
		return nil
	}
	if !suffixRegex.MatchString(suffix) {
		return fmt.Errorf("invalid filenameSuffix %q: must contain only letters, numbers, underscores, or hyphens", suffix)
	}
	return nil
}

// ResolveTargetDir resolves the target directory for image downloads.
// When rawDir is empty, it defaults to os.TempDir() as requested.
// Rejects Windows drive letters on POSIX systems to prevent accidental relative paths.
func ResolveTargetDir(rawDir string) (string, error) {
	rawDir = strings.TrimSpace(rawDir)
	if rawDir == "" {
		return filepath.Clean(os.TempDir()), nil
	}
	if runtime.GOOS != "windows" && driveLetterOnPOSIX.MatchString(rawDir) {
		return "", fmt.Errorf("invalid directory %q: Windows drive-letter paths are not supported on %s", rawDir, runtime.GOOS)
	}
	if runtime.GOOS != "windows" {
		rawDir = strings.ReplaceAll(rawDir, `\`, `/`)
	}
	abs, err := filepath.Abs(rawDir)
	if err != nil {
		return "", fmt.Errorf("resolve directory %q: %w", rawDir, err)
	}
	return filepath.Clean(abs), nil
}

// ResolveSafeFilePath joins targetDir and fileName and verifies the resulting path stays strictly inside targetDir.
func ResolveSafeFilePath(targetDir, fileName string) (string, error) {
	if err := ValidateFileName(fileName); err != nil {
		return "", err
	}
	base, err := filepath.Abs(targetDir)
	if err != nil {
		return "", err
	}
	candidate := filepath.Clean(filepath.Join(base, fileName))
	rel, err := filepath.Rel(base, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("file path %q escapes target directory %q", fileName, base)
	}
	return candidate, nil
}

// ApplyFilenameSuffix appends "-<suffix>" before the file extension if not already present.
func ApplyFilenameSuffix(fileName, suffix string) string {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return fileName
	}
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	if strings.HasSuffix(base, "-"+suffix) {
		return fileName
	}
	return fmt.Sprintf("%s-%s%s", base, suffix, ext)
}

// ParseSVGDimensions extracts pixel width and height from an SVG file's root <svg> attributes or viewBox.
func ParseSVGDimensions(svgContent string) Dimensions {
	tag := svgTagRegex.FindString(svgContent)
	if tag == "" {
		return Dimensions{}
	}

	wMatch := svgWidthRegex.FindStringSubmatch(tag)
	hMatch := svgHeightRegex.FindStringSubmatch(tag)
	if len(wMatch) > 1 && len(hMatch) > 1 {
		wStr, hStr := strings.TrimSpace(wMatch[1]), strings.TrimSpace(hMatch[1])
		if !strings.Contains(wStr, "%") && !strings.Contains(hStr, "%") {
			w, errW := parseSVGNumber(wStr)
			h, errH := parseSVGNumber(hStr)
			if errW == nil && errH == nil && w > 0 && h > 0 {
				return Dimensions{Width: int(math.Round(w)), Height: int(math.Round(h))}
			}
		}
	}

	vbMatch := svgViewBoxRegex.FindStringSubmatch(tag)
	if len(vbMatch) > 1 {
		parts := splitViewBoxRegex.Split(strings.TrimSpace(vbMatch[1]), -1)
		if len(parts) >= 4 {
			w, errW := strconv.ParseFloat(parts[2], 64)
			h, errH := strconv.ParseFloat(parts[3], 64)
			if errW == nil && errH == nil && w > 0 && h > 0 {
				return Dimensions{Width: int(math.Round(w)), Height: int(math.Round(h))}
			}
		}
	}
	return Dimensions{}
}

func parseSVGNumber(s string) (float64, error) {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "px")
	return strconv.ParseFloat(s, 64)
}

// GenerateImageCSSVariables formats --original-width and --original-height CSS variables for TILE fills.
func GenerateImageCSSVariables(d Dimensions) string {
	return fmt.Sprintf("--original-width: %dpx; --original-height: %dpx;", d.Width, d.Height)
}

type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

// ProcessDownloadedImage inspects dimensions of an image on disk (SVG, GIF, PNG, JPEG)
// and applies 2x3 affine matrix cropping when needsCropping is true (for non-GIF/non-SVG raster images).
func ProcessDownloadedImage(filePath string, needsCropping bool, cropTransform [][]float64) (Dimensions, bool, error) {
	lower := strings.ToLower(filePath)
	if strings.HasSuffix(lower, ".svg") {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return Dimensions{}, false, fmt.Errorf("read svg %s: %w", filePath, err)
		}
		return ParseSVGDimensions(string(b)), false, nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return Dimensions{}, false, fmt.Errorf("open image %s: %w", filePath, err)
	}
	img, format, decodeErr := image.Decode(f)
	_ = f.Close()
	if decodeErr != nil {
		return Dimensions{}, false, fmt.Errorf("decode image %s: %w", filePath, decodeErr)
	}

	bounds := img.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	// Do not crop animated GIFs or when cropTransform is absent
	if !needsCropping || len(cropTransform) < 2 || strings.HasSuffix(lower, ".gif") {
		return Dimensions{Width: origW, Height: origH}, false, nil
	}

	scaleX, translateX := 1.0, 0.0
	scaleY, translateY := 1.0, 0.0
	if len(cropTransform[0]) >= 3 {
		scaleX = cropTransform[0][0]
		translateX = cropTransform[0][2]
	}
	if len(cropTransform[1]) >= 3 {
		scaleY = cropTransform[1][1]
		translateY = cropTransform[1][2]
	}

	cropLeft := int(math.Max(0, math.Round(translateX*float64(origW))))
	cropTop := int(math.Max(0, math.Round(translateY*float64(origH))))
	cropW := int(math.Min(float64(origW-cropLeft), math.Round(scaleX*float64(origW))))
	cropH := int(math.Min(float64(origH-cropTop), math.Round(scaleY*float64(origH))))

	if cropW <= 0 || cropH <= 0 || (cropLeft == 0 && cropTop == 0 && cropW == origW && cropH == origH) {
		return Dimensions{Width: origW, Height: origH}, false, nil
	}

	si, ok := img.(subImager)
	if !ok {
		return Dimensions{Width: origW, Height: origH}, false, nil
	}

	cropRect := image.Rect(bounds.Min.X+cropLeft, bounds.Min.Y+cropTop, bounds.Min.X+cropLeft+cropW, bounds.Min.Y+cropTop+cropH)
	croppedImg := si.SubImage(cropRect)

	outFile, err := os.Create(filePath)
	if err != nil {
		return Dimensions{}, false, fmt.Errorf("recreate image %s for cropping: %w", filePath, err)
	}
	var encErr error
	switch format {
	case "jpeg", "jpg":
		encErr = jpeg.Encode(outFile, croppedImg, &jpeg.Options{Quality: 95})
	default:
		encErr = png.Encode(outFile, croppedImg)
	}
	closeErr := outFile.Close()
	if encErr != nil || closeErr != nil {
		return Dimensions{}, false, fmt.Errorf("encode cropped image %s: %v", filePath, encErr)
	}

	return Dimensions{Width: cropW, Height: cropH}, true, nil
}
