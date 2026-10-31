package mcp

import (
	"bytes"
	"context"
	"encoding/json"
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
)

func TestMCPServerProtocolAndTools(t *testing.T) {
	// Prepare synthetic PNG for screenshot / asset download tests
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: 50, G: 100, B: 200, A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/files/abc123XYZ/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "MCP Design System",
			"nodes": {
				"12:34": {
					"document": {
						"id": "12:34",
						"name": "ActionCard",
						"type": "FRAME",
						"layoutMode": "VERTICAL",
						"transitionNodeID": "12:99",
						"transitionDuration": 0.35,
						"transitionEasing": "EASE_OUT",
						"children": [
							{"id": "12:35", "name": "ic-check", "type": "VECTOR"}
						]
					},
					"components": {
						"12:34": {"id": "12:34", "key": "comp_card", "name": "Cards/ActionCard"}
					}
				}
			}
		}`))
	})
	mux.HandleFunc("/files/abc123XYZ/variables/local", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": 200,
			"error": false,
			"meta": {
				"variableCollections": {
					"col-1": {"id": "col-1", "name": "Core", "defaultModeId": "m1", "modes": [{"modeId": "m1", "name": "Light"}]}
				},
				"variables": {
					"v1": {
						"id": "v1",
						"name": "Colors/Primary",
						"variableCollectionId": "col-1",
						"resolvedType": "COLOR",
						"valuesByMode": {"m1": {"r": 0.2, "g": 0.4, "b": 0.8, "a": 1.0}}
					}
				}
			}
		}`))
	})
	mux.HandleFunc("/images/abc123XYZ", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		format := r.URL.Query().Get("format")
		ids := r.URL.Query().Get("ids")
		if format == "svg" {
			_, _ = w.Write([]byte(`{"images":{"` + ids + `":"` + srv.URL + `/icon.svg"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"images":{"` + ids + `":"` + srv.URL + `/shot.png"}}`))
	})
	mux.HandleFunc("/files/abc123XYZ/images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":{"images":{}}}`))
	})
	mux.HandleFunc("/shot.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBuf.Bytes())
	})
	mux.HandleFunc("/icon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(`<svg width="16" height="16" viewBox="0 0 16 16"></svg>`))
	})
	mux.HandleFunc("/files/secretFail/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"err":"forbidden token figd_SUPER_SECRET_123"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	// Create sample component for code_connect tool
	compDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(compDir, "ActionCard.tsx"), []byte(`export function ActionCard() { return <div/>; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	shotOut := filepath.Join(t.TempDir(), "shot.png")
	assetDir := t.TempDir()

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_design_context","arguments":{"url":"https://www.figma.com/design/abc123XYZ/App?node-id=12-34","format":"tree"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_screenshot","arguments":{"fileKey":"abc123XYZ","nodeId":"12:34","outputPath":"` + shotOut + `"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_tokens","arguments":{"fileKey":"abc123XYZ","format":"tailwind-v4"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_motion_context","arguments":{"fileKey":"abc123XYZ","nodeId":"12:34","format":"framer"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"download_assets","arguments":{"fileKey":"abc123XYZ","nodeId":"12:34","dir":"` + assetDir + `"}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"code_connect","arguments":{"fileKey":"abc123XYZ","nodeId":"12:34","dir":"` + compDir + `","generate":true}}}`,
		`not-valid-json`,
		`{"jsonrpc":"2.0","id":10,"method":"unknown/method"}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"get_design_context","arguments":{"fileKey":"secretFail","nodeId":"12:34"}}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"get_screenshot","arguments":{"fileKey":"abc123XYZ","nodeId":"12:34","format":"../../evil"}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"get_screenshot","arguments":{"fileKey":"abc123XYZ","nodeId":"12:34","outputPath":"` + assetDir + `"}}}`,
	}

	inBuf := bytes.NewBufferString(strings.Join(requests, "\n") + "\n")
	var outBuf, errBuf bytes.Buffer

	mcpSrv := NewServer(Options{
		Auth:    config.AuthConfig{APIKey: "figd_SUPER_SECRET_123"},
		BaseURL: srv.URL,
	}, inBuf, &outBuf, &errBuf)

	if err := mcpSrv.Serve(context.Background()); err != nil {
		t.Fatalf("mcpSrv.Serve failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	// 14 responses expected (all except notification)
	if len(lines) != 14 {
		t.Fatalf("expected 14 JSON-RPC responses, got %d:\n%s", len(lines), outBuf.String())
	}

	// Check tools/list (response #3 -> index 2)
	if !strings.Contains(lines[2], "get_design_context") || !strings.Contains(lines[2], "get_screenshot") ||
		!strings.Contains(lines[2], "get_tokens") || !strings.Contains(lines[2], "get_motion_context") ||
		!strings.Contains(lines[2], "download_assets") || !strings.Contains(lines[2], "code_connect") {
		t.Errorf("tools/list missing expected tools:\n%s", lines[2])
	}

	// Check get_design_context (index 3)
	if !strings.Contains(lines[3], "ActionCard") {
		t.Errorf("get_design_context missing ActionCard:\n%s", lines[3])
	}

	// Check get_screenshot (index 4)
	if _, err := os.Stat(shotOut); err != nil {
		t.Errorf("expected screenshot file at %s: %v", shotOut, err)
	}

	// Check get_tokens (index 5)
	if !strings.Contains(lines[5], "@theme") || !strings.Contains(lines[5], "#3366CC") {
		t.Errorf("get_tokens missing @theme:\n%s", lines[5])
	}

	// Check get_motion_context (index 6)
	if !strings.Contains(lines[6], "motionTransitions") || !strings.Contains(lines[6], "0.35") {
		t.Errorf("get_motion_context unexpected output:\n%s", lines[6])
	}

	// Check download_assets (index 7)
	if !strings.Contains(lines[7], "Downloaded") {
		t.Errorf("download_assets unexpected output:\n%s", lines[7])
	}

	// Check code_connect (index 8)
	if !strings.Contains(lines[8], "ActionCard.figma.ts") {
		t.Errorf("code_connect unexpected output:\n%s", lines[8])
	}

	// Check parse error (-32700) on malformed line (index 9)
	if !strings.Contains(lines[9], "-32700") {
		t.Errorf("expected -32700 parse error on index 9:\n%s", lines[9])
	}

	// Check method not found (-32601) (index 10)
	if !strings.Contains(lines[10], "-32601") {
		t.Errorf("expected -32601 method not found on index 10:\n%s", lines[10])
	}

	// Check secret redaction on error response (index 11)
	if strings.Contains(lines[11], "figd_SUPER_SECRET_123") || !strings.Contains(lines[11], "[REDACTED]") {
		t.Errorf("secret token was not redacted in MCP tool error response:\n%s", lines[11])
	}

	var resp11 map[string]any
	if err := json.Unmarshal([]byte(lines[11]), &resp11); err != nil {
		t.Fatalf("unmarshal resp11: %v", err)
	}

	// Check invalid screenshot format rejection (index 12)
	if !strings.Contains(lines[12], `"isError":true`) || !strings.Contains(lines[12], "unsupported screenshot format") {
		t.Errorf("expected unsupported screenshot format error on index 12:\n%s", lines[12])
	}

	// Check directory resolution for get_screenshot (index 13)
	if !strings.Contains(lines[13], "screenshot-abc123XYZ-12_34.png") || strings.Contains(lines[13], `"isError":true`) {
		t.Errorf("expected screenshot saved to directory without error on index 13:\n%s", lines[13])
	}
	expectedFile := filepath.Join(assetDir, "screenshot-abc123XYZ-12_34.png")
	if _, statErr := os.Stat(expectedFile); statErr != nil {
		t.Errorf("expected screenshot file at %s: %v", expectedFile, statErr)
	}
}
