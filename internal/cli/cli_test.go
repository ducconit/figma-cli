package cli

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func executeCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestVersionAndHelpCommands(t *testing.T) {
	// 1. fm -v
	out, err := executeCLI(t, "-v")
	if err != nil {
		t.Fatalf("fm -v failed: %v", err)
	}
	if !strings.Contains(out, "fm version") {
		t.Fatalf("unexpected fm -v output: %s", out)
	}

	// 2. fm version --json
	outJSON, err := executeCLI(t, "version", "--json")
	if err != nil {
		t.Fatalf("fm version --json failed: %v", err)
	}
	if !strings.Contains(outJSON, `"version"`) || !strings.Contains(outJSON, `"os"`) {
		t.Fatalf("unexpected fm version --json output: %s", outJSON)
	}

	// 3. fm -h and subcommands -h
	for _, sub := range [][]string{{"-h"}, {"fetch", "-h"}, {"download", "-h"}, {"install", "-h"}, {"set-token", "-h"}, {"update", "-h"}} {
		helpOut, err := executeCLI(t, sub...)
		if err != nil {
			t.Fatalf("fm %v failed: %v", sub, err)
		}
		if !strings.Contains(helpOut, "Usage:") {
			t.Fatalf("expected Usage in fm %v output:\n%s", sub, helpOut)
		}
	}
}

func TestFetchDownloadAndInstallCLIIntegration(t *testing.T) {
	// Create synthetic PNG for download test
	img := image.NewRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
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
			"name": "CLI Demo App",
			"nodes": {
				"12:34": {
					"document": {
						"id": "12:34",
						"name": "HeroBanner",
						"type": "FRAME",
						"clipsContent": true,
						"layoutMode": "HORIZONTAL",
						"itemSpacing": 16,
						"children": [
							{
								"id": "12:35",
								"name": "Title",
								"type": "TEXT",
								"characters": "Welcome to Figma CLI",
								"style": {"fontFamily": "Inter", "fontWeight": 700, "fontSize": 24}
							},
							{
								"id": "12:36",
								"name": "ic-star",
								"type": "VECTOR"
							}
						]
					}
				}
			}
		}`))
	})
	mux.HandleFunc("/images/abc123XYZ", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"images":{"12:36":"` + srv.URL + `/asset/ic-star.svg"}}`))
	})
	mux.HandleFunc("/files/abc123XYZ/images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":{"images":{"ref1":"` + srv.URL + `/asset/photo.png"}}}`))
	})
	mux.HandleFunc("/asset/ic-star.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(`<svg width="32" height="32" viewBox="0 0 32 32"></svg>`))
	})
	mux.HandleFunc("/asset/photo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBuf.Bytes())
	})

	srv = httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("FIGMA_API_KEY", "env_secret_token_abcd")

	// 1. Test `fm fetch` reading token from env (tree, json, and -o output file)
	fetchOut, err := executeCLI(t, "fetch", "https://www.figma.com/design/abc123XYZ/Demo?node-id=12-34", "--api-base-url", srv.URL, "--proxy", "none")
	if err != nil {
		t.Fatalf("fm fetch failed: %v\nOutput: %s", err, fetchOut)
	}
	if !strings.Contains(fetchOut, `NAME: "CLI Demo App"`) || !strings.Contains(fetchOut, `[IMAGE-SVG] "ic-star" #12:36`) {
		t.Fatalf("unexpected fm fetch tree output:\n%s", fetchOut)
	}

	jsonOut, err := executeCLI(t, "fetch", "https://www.figma.com/design/abc123XYZ/Demo?node-id=12-34", "--json", "--api-base-url", srv.URL, "--proxy", "none")
	if err != nil || !strings.Contains(jsonOut, `"CLI Demo App"`) {
		t.Fatalf("fm fetch --json failed: %v\nOutput: %s", err, jsonOut)
	}

	outFile := filepath.Join(t.TempDir(), "out", "design.yaml")
	fileOut, err := executeCLI(t, "fetch", "https://www.figma.com/design/abc123XYZ/Demo?node-id=12-34", "-f", "yaml", "-o", outFile, "--api-base-url", srv.URL, "--proxy", "none")
	if err != nil || !strings.Contains(fileOut, "Saved simplified design") {
		t.Fatalf("fm fetch -o failed: %v\nOutput: %s", err, fileOut)
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Fatalf("expected saved file at %s: %v", outFile, err)
	}

	// Auto-discover download (no --item or --manifest)
	autoDlDir := t.TempDir()
	autoDlOut, err := executeCLI(t, "download", "https://www.figma.com/design/abc123XYZ/Demo?node-id=12-34",
		"--dir", autoDlDir,
		"--api-base-url", srv.URL,
		"--proxy", "none",
	)
	if err != nil || !strings.Contains(autoDlOut, "svg-12_36.svg: 32x32") {
		t.Fatalf("auto-discover fm download failed: %v\nOutput: %s", err, autoDlOut)
	}

	// 2. Test `fm download` to custom --dir
	dlDir := t.TempDir()
	dlOut, err := executeCLI(t, "download", "https://www.figma.com/design/abc123XYZ/Demo?node-id=12-34",
		"--dir", dlDir,
		"--item", "nodeId=12:36,file=ic-star.svg",
		"--item", "nodeId=12:34,imageRef=ref1,file=photo.png",
		"--api-base-url", srv.URL,
		"--proxy", "none",
	)
	if err != nil {
		t.Fatalf("fm download failed: %v\nOutput: %s", err, dlOut)
	}
	if !strings.Contains(dlOut, "ic-star.svg: 32x32") || !strings.Contains(dlOut, "photo.png: 80x40") {
		t.Fatalf("unexpected fm download output:\n%s", dlOut)
	}
	if _, err := os.Stat(filepath.Join(dlDir, "ic-star.svg")); err != nil {
		t.Fatalf("expected ic-star.svg on disk: %v", err)
	}

	// 3. Test `fm install` with default ~/.agents/skills/figma-cli and --agy / --claude / --codex symlinks
	fakeHome := t.TempDir()
	installOut, err := executeCLI(t, "install", "--home-dir", fakeHome, "--agy", "--claude", "--codex", "--token", "figd_custom_install_token")
	if err != nil {
		t.Fatalf("fm install failed: %v\nOutput: %s", err, installOut)
	}
	skillPath := filepath.Join(fakeHome, ".agents", "skills", "figma-cli", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Fatalf("expected installed SKILL.md at %s: %v", skillPath, err)
	}
	agySymlink := filepath.Join(fakeHome, ".gemini", "config", "skills", "figma-cli", "SKILL.md")
	if _, err := os.Stat(agySymlink); err != nil {
		t.Fatalf("expected Antigravity symlinked SKILL.md at %s: %v", agySymlink, err)
	}

	// 4. Test `fm install --path .agent/skills` for project-local installation
	projRoot := t.TempDir()
	projSkills := filepath.Join(projRoot, ".agent", "skills")
	projOut, err := executeCLI(t, "install", "--path", projSkills, "--home-dir", fakeHome)
	if err != nil {
		t.Fatalf("fm install --path failed: %v\nOutput: %s", err, projOut)
	}
	if _, err := os.Stat(filepath.Join(projSkills, "figma-cli", "SKILL.md")); err != nil {
		t.Fatalf("expected project SKILL.md in %s: %v", projSkills, err)
	}

	// 5. Test `fm set-token <token>` updating ~/.figma/.env exclusively
	setTokOut, err := executeCLI(t, "set-token", "figd_set_token_987654", "--home-dir", fakeHome)
	if err != nil {
		t.Fatalf("fm set-token failed: %v\nOutput: %s", err, setTokOut)
	}
	homeEnvBytes, err := os.ReadFile(filepath.Join(fakeHome, ".figma", ".env"))
	if err != nil {
		t.Fatalf("expected ~/.figma/.env to exist: %v", err)
	}
	if !strings.Contains(string(homeEnvBytes), "FIGMA_API_KEY=figd_set_token_987654") {
		t.Fatalf("expected updated FIGMA_API_KEY in ~/.figma/.env, got:\n%s", string(homeEnvBytes))
	}
}

func TestUpdateCLICommand(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"tag_name": "v9.9.9",
				"name": "v9.9.9 Release",
				"assets": [
					{"name": "fm-linux-amd64", "browser_download_url": "https://example.com/fm-linux-amd64", "size": 1024},
					{"name": "fm-darwin-arm64", "browser_download_url": "https://example.com/fm-darwin-arm64", "size": 1024},
					{"name": "fm-windows-amd64.exe", "browser_download_url": "https://example.com/fm-windows-amd64.exe", "size": 1024}
				]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	// 1. Check update
	out, err := executeCLI(t, "update", "--check", "--test-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm update --check failed: %v", err)
	}
	if !strings.Contains(out, "v9.9.9") {
		t.Fatalf("expected v9.9.9 in update output, got:\n%s", out)
	}

	// 2. Check update with --json
	outJSON, err := executeCLI(t, "update", "--check", "--json", "--test-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm update --check --json failed: %v", err)
	}
	if !strings.Contains(outJSON, `"hasUpdate": true`) || !strings.Contains(outJSON, `"latestVersion": "9.9.9"`) {
		t.Fatalf("unexpected json output:\n%s", outJSON)
	}
}
