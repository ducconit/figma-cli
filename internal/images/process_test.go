package images

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ducconit/figma-cli/internal/config"
	"github.com/ducconit/figma-cli/internal/extractors"
	"github.com/ducconit/figma-cli/internal/figma"
)

func createSyntheticPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownloadDedupCropAndSVGDimensions(t *testing.T) {
	pngBytes := createSyntheticPNG(t, 200, 100)
	svgString := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="100%" height="100%"><path d="M0 0h24v24H0z"/></svg>`

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/files/file123/images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":{"images":{"ref_hero":"` + srv.URL + `/cdn/hero.png"}}}`))
	})
	mux.HandleFunc("/images/file123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"images":{"10:1":"` + srv.URL + `/cdn/icon.svg"}}`))
	})
	mux.HandleFunc("/cdn/hero.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	})
	mux.HandleFunc("/cdn/icon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(svgString))
	})

	srv = httptest.NewServer(mux)
	defer srv.Close()

	client, err := figma.NewClient(config.AuthConfig{APIKey: "test_pat"}, "none")
	if err != nil {
		t.Fatal(err)
	}
	client.SetBaseURL(srv.URL)

	tmpOut := t.TempDir()
	results, err := DownloadFigmaImages(context.Background(), client, DownloadParams{
		FileKey:    "file123",
		TargetDir:  tmpOut,
		PNGScale:   2,
		SVGOptions: figma.DefaultSVGOptions(),
		Nodes: []DownloadNodeRequest{
			// 1. Uncropped imageRef + duplicate alias + TILE dimensions
			{
				NodeID:                  "1:1",
				ImageRef:                "ref_hero",
				FileName:                "hero.png",
				RequiresImageDimensions: true,
			},
			{
				NodeID:   "1:2",
				ImageRef: "ref_hero",
				FileName: "hero-duplicate.png",
			},
			// 2. Cropped variant of the same imageRef (50% width, 50% height)
			{
				NodeID:         "1:3",
				ImageRef:       "ref_hero",
				FileName:       "hero.png",
				FilenameSuffix: "crop50",
				NeedsCropping:  true,
				CropTransform: [][]float64{
					{0.5, 0, 0.25},
					{0, 0.5, 0.25},
				},
			},
			// 3. Rendered SVG node with viewBox fallback
			{
				NodeID:   "10-1",
				FileName: "icon.svg",
			},
		},
	})
	if err != nil {
		t.Fatalf("DownloadFigmaImages failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 deduplicated download results, got %d: %+v", len(results), results)
	}

	// Verify 1: Uncropped + CSS variables + alias
	r0 := results[0]
	if r0.Dimensions.Width != 200 || r0.Dimensions.Height != 100 || r0.WasCropped {
		t.Fatalf("unexpected r0 dimensions: %+v", r0)
	}
	if r0.CSSVariables != "--original-width: 200px; --original-height: 100px;" {
		t.Fatalf("unexpected CSS variables: %q", r0.CSSVariables)
	}
	if len(r0.Aliases) != 1 || r0.Aliases[0] != "hero-duplicate.png" {
		t.Fatalf("expected alias hero-duplicate.png, got %+v", r0.Aliases)
	}

	// Verify 2: Cropped variant (200x100 * 0.5 -> 100x50)
	r1 := results[1]
	if r1.FileName != "hero-crop50.png" || !r1.WasCropped || r1.Dimensions.Width != 100 || r1.Dimensions.Height != 50 {
		t.Fatalf("expected cropped 100x50 hero-crop50.png, got %+v", r1)
	}

	// Verify 3: SVG viewBox dimensions (24x24)
	r2 := results[2]
	if r2.FileName != "icon.svg" || r2.Dimensions.Width != 24 || r2.Dimensions.Height != 24 {
		t.Fatalf("expected 24x24 SVG dimensions, got %+v", r2)
	}
}

func TestPathSecurityAndDefaultTempDir(t *testing.T) {
	// Default empty dir resolves to os.TempDir()
	defDir, err := ResolveTargetDir("")
	if err != nil || defDir != filepath.Clean(os.TempDir()) {
		t.Fatalf("expected default os.TempDir() %q, got %q (err=%v)", os.TempDir(), defDir, err)
	}

	// Reject path traversal in fileName
	if _, err := ResolveSafeFilePath(t.TempDir(), "../evil.png"); err == nil {
		t.Fatal("expected error for ../evil.png")
	}
	if err := ValidateFileName("sub/evil.png"); err == nil {
		t.Fatal("expected error for slash in fileName")
	}
	if !strings.Contains(GenerateImageCSSVariables(Dimensions{Width: 10, Height: 20}), "--original-width: 10px;") {
		t.Fatal("unexpected CSS variable output")
	}
}

func TestParseQuickItemManifestAndDiscoverNodes(t *testing.T) {
	// 1. ParseQuickItem: key=value, shorthand, bare nodeId, and error cases
	kvReq, err := ParseQuickItem("nodeId=12-34,file=hero.png,imageRef=ref1,gifRef=gif1,suffix=crp1")
	if err != nil || kvReq.NodeID != "12:34" || kvReq.FileName != "hero.png" || kvReq.ImageRef != "ref1" || kvReq.GifRef != "gif1" || kvReq.FilenameSuffix != "crp1" {
		t.Fatalf("unexpected kvReq: %+v (err=%v)", kvReq, err)
	}
	shortReq, err := ParseQuickItem("10-20=ic-bell.svg")
	if err != nil || shortReq.NodeID != "10:20" || shortReq.FileName != "ic-bell.svg" {
		t.Fatalf("unexpected shortReq: %+v (err=%v)", shortReq, err)
	}
	bareReq, err := ParseQuickItem("15-30;1-2")
	if err != nil || bareReq.NodeID != "15:30;1:2" || bareReq.FileName != "15_30_1_2.png" {
		t.Fatalf("unexpected bareReq: %+v (err=%v)", bareReq, err)
	}
	if _, err := ParseQuickItem(""); err == nil {
		t.Fatal("expected error for empty --item")
	}
	if _, err := ParseQuickItem("nodeId=12:34"); err == nil {
		t.Fatal("expected error for missing file= in --item")
	}

	// 2. ParseManifest: inline JSON array, inline JSON object, and JSON file path
	arrReqs, err := ParseManifest(`[{"nodeId":"1:1","fileName":"a.svg"},{"nodeId":"1:2","fileName":"b.png"}]`)
	if err != nil || len(arrReqs) != 2 {
		t.Fatalf("unexpected arrReqs: %+v (err=%v)", arrReqs, err)
	}
	objReqs, err := ParseManifest(`{"nodeId":"1:1","fileName":"single.svg"}`)
	if err != nil || len(objReqs) != 1 || objReqs[0].FileName != "single.svg" {
		t.Fatalf("unexpected objReqs: %+v (err=%v)", objReqs, err)
	}
	manifestFile := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestFile, []byte(`[{"nodeId":"9:9","fileName":"from-file.png"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileReqs, err := ParseManifest(manifestFile)
	if err != nil || len(fileReqs) != 1 || fileReqs[0].FileName != "from-file.png" {
		t.Fatalf("unexpected fileReqs: %+v (err=%v)", fileReqs, err)
	}

	// 3. FormatLine
	line := (DownloadResult{
		FileName:     "hero.png",
		Dimensions:   Dimensions{Width: 400, Height: 200},
		CSSVariables: "--original-width: 400px; --original-height: 200px;",
		WasCropped:   true,
		Aliases:      []string{"hero-copy.png"},
	}).FormatLine()
	if !strings.Contains(line, "400x200") || !strings.Contains(line, "(cropped)") || !strings.Contains(line, "hero-copy.png") {
		t.Fatalf("unexpected FormatLine: %s", line)
	}

	// 4. DiscoverDownloadableNodes (IMAGE-SVG, IMAGE fill via GlobalVars, PATTERN fill, and ElementBody template)
	design := &extractors.SimplifiedDesign{
		GlobalVars: extractors.GlobalVars{
			Styles: map[string]any{
				"fill_shared": []any{
					map[string]any{
						"type":     "IMAGE",
						"imageRef": "1234567890abcdef",
						"imageDownloadArguments": map[string]any{
							"needsCropping":           true,
							"requiresImageDimensions": true,
							"filenameSuffix":          "abc123",
							"cropTransform":           [][]float64{{0.5, 0, 0}, {0, 0.5, 0}},
						},
					},
					map[string]any{
						"type": "PATTERN",
						"patternSource": map[string]any{
							"nodeId": "88:99",
						},
					},
				},
			},
		},
		Elements: map[string]extractors.ElementBody{
			"EL-icon": {
				"type": "IMAGE-SVG",
			},
		},
		Nodes: []*extractors.SimplifiedNode{
			{
				ID:       "10:1",
				Template: "EL-icon",
			},
			{
				ID:    "10:2",
				Type:  "FRAME",
				Fills: "fill_shared",
				Children: []*extractors.SimplifiedNode{
					{
						ID:   "10:3",
						Type: "RECTANGLE",
						Fills: []any{
							map[string]any{
								"type":   "IMAGE",
								"gifRef": "gif_anim_99",
							},
						},
					},
				},
			},
		},
	}

	discovered := DiscoverDownloadableNodes(design)
	if len(discovered) != 4 {
		t.Fatalf("expected 4 discovered downloadable nodes (SVG, IMAGE, PATTERN, GIF), got %d: %+v", len(discovered), discovered)
	}
}
