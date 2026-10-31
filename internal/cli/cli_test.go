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
	for _, sub := range [][]string{
		{"-h"},
		{"fetch", "-h"},
		{"download", "-h"},
		{"screenshot", "-h"},
		{"motion", "-h"},
		{"tokens", "-h"},
		{"code-connect", "-h"},
		{"link", "-h"},
		{"mcp", "-h"},
		{"install", "-h"},
		{"set-token", "-h"},
		{"update", "-h"},
		{"stitch", "-h"},
	} {
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

func TestTokensCLICommand(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/variables/local") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status": 200,
				"error": false,
				"meta": {
					"variables": {
						"v-1": {
							"id": "v-1",
							"name": "Colors/Brand",
							"variableCollectionID": "col-1",
							"resolvedType": "COLOR",
							"valuesByMode": {
								"m-light": {"r": 0.1, "g": 0.2, "b": 0.8, "a": 1.0}
							}
						}
					},
					"variableCollections": {
						"col-1": {
							"id": "col-1",
							"name": "Global",
							"defaultModeId": "m-light",
							"modes": [{"modeId": "m-light", "name": "Light"}]
						}
					}
				}
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	// 1. fm tokens with URL and default CSS output
	outCSS, err := executeCLI(t, "tokens", "https://www.figma.com/design/abc123XYZ/Tokens", "--token", "tok123", "--api-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm tokens failed: %v\nOutput: %s", err, outCSS)
	}
	if !strings.Contains(outCSS, ":root {") || !strings.Contains(outCSS, "--colors-brand:") {
		t.Fatalf("unexpected CSS output:\n%s", outCSS)
	}

	// 2. fm tokens with --format tailwind
	outTW, err := executeCLI(t, "tokens", "--file-key", "abc123XYZ", "--format", "tailwind", "--token", "tok123", "--api-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm tokens --format tailwind failed: %v", err)
	}
	if !strings.Contains(outTW, "module.exports") || !strings.Contains(outTW, "colors:") {
		t.Fatalf("unexpected Tailwind output:\n%s", outTW)
	}

	// 3. fm tokens -o <file> with --format json
	destFile := filepath.Join(t.TempDir(), "tokens.json")
	outSave, err := executeCLI(t, "tokens", "--file-key", "abc123XYZ", "--format", "json", "-o", destFile, "--token", "tok123", "--api-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm tokens -o failed: %v\nOutput: %s", err, outSave)
	}
	data, err := os.ReadFile(destFile)
	if err != nil || !strings.Contains(string(data), `"collections"`) {
		t.Fatalf("failed to read written tokens file: %v (data=%s)", err, string(data))
	}
}

func TestLinkCLICommand(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/dev_resources/res-1") && r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/dev_resources") {
			switch r.Method {
			case http.MethodGet:
				_, _ = w.Write([]byte(`{
					"links": [
						{"id": "res-1", "name": "PR 42", "url": "https://github.com/org/repo/pull/42", "file_key": "abc123XYZ", "node_id": "12:34"}
					]
				}`))
			case http.MethodPost:
				_, _ = w.Write([]byte(`{
					"links": [
						{"id": "res-new", "name": "Storybook", "url": "https://storybook.example.com", "file_key": "abc123XYZ", "node_id": "12:34"}
					]
				}`))
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	// 1. fm link create with URL and node-id
	outCreate, err := executeCLI(t, "link", "https://www.figma.com/design/abc123XYZ/App?node-id=12-34",
		"--url", "https://storybook.example.com", "--name", "Storybook", "--token", "tok123", "--api-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm link create failed: %v\nOutput: %s", err, outCreate)
	}
	if !strings.Contains(outCreate, "Attached dev resource") || !strings.Contains(outCreate, "Storybook") {
		t.Fatalf("unexpected link create output:\n%s", outCreate)
	}

	// 2. fm link --list
	outList, err := executeCLI(t, "link", "--file-key", "abc123XYZ", "--list", "--token", "tok123", "--api-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm link --list failed: %v", err)
	}
	if !strings.Contains(outList, "PR 42") || !strings.Contains(outList, "https://github.com/org/repo/pull/42") {
		t.Fatalf("unexpected link list output:\n%s", outList)
	}

	// 3. fm link --delete
	outDel, err := executeCLI(t, "link", "--file-key", "abc123XYZ", "--delete", "res-1", "--token", "tok123", "--api-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm link --delete failed: %v", err)
	}
	if !strings.Contains(outDel, "Deleted dev resource res-1") {
		t.Fatalf("unexpected link delete output:\n%s", outDel)
	}
}

func TestScreenshotCLICommand(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/files/abc123XYZ", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "Demo Screen",
			"document": {
				"id": "0:0",
				"type": "DOCUMENT",
				"children": [
					{
						"id": "1:1",
						"name": "Page 1",
						"type": "CANVAS",
						"children": [
							{"id": "10:99", "name": "LoginFrame", "type": "FRAME"}
						]
					}
				]
			}
		}`))
	})
	mux.HandleFunc("/images/abc123XYZ", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ids := r.URL.Query().Get("ids")
		if ids == "99:99" {
			// Simulate empty render URL failure mode (Review Focus #2)
			_, _ = w.Write([]byte(`{"images":{"99:99":null}}`))
			return
		}
		_, _ = w.Write([]byte(`{"images":{"` + ids + `":"` + srv.URL + `/render.png"}}`))
	})
	mux.HandleFunc("/render.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("fake-png-bytes"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	// 1. Explicit node-id screenshot with --json
	outPath := filepath.Join(t.TempDir(), "hero.png")
	outJSON, err := executeCLI(t, "screenshot", "https://www.figma.com/design/abc123XYZ/App?node-id=12-34",
		"-o", outPath, "--scale", "2", "--json", "--token", "tok123", "--api-base-url", srv.URL)
	if err != nil {
		t.Fatalf("fm screenshot failed: %v\nOutput: %s", err, outJSON)
	}
	if !strings.Contains(outJSON, `"nodeId": "12:34"`) || !strings.Contains(outJSON, `"filePath"`) {
		t.Fatalf("unexpected screenshot JSON output:\n%s", outJSON)
	}
	if _, statErr := os.Stat(outPath); statErr != nil {
		t.Fatalf("expected screenshot file at %s: %v", outPath, statErr)
	}

	// 2. Auto-discover top-level frame when node-id is omitted
	autoDir := t.TempDir()
	outAuto, err := executeCLI(t, "screenshot", "abc123XYZ", "-o", autoDir, "--token", "tok123", "--api-base-url", srv.URL)
	if err != nil {
		t.Fatalf("fm screenshot auto-discover failed: %v\nOutput: %s", err, outAuto)
	}
	if !strings.Contains(outAuto, "10:99") {
		t.Fatalf("expected auto-discovered frame 10:99 in output:\n%s", outAuto)
	}

	// 3. Empty render URL failure mode
	if _, err := executeCLI(t, "screenshot", "abc123XYZ", "--node-id", "99:99", "--token", "tok123", "--api-base-url", srv.URL); err == nil {
		t.Fatal("expected error when Figma returns null render URL")
	}
}

func TestMotionCodeConnectAndMCPCLICommands(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/files/abc123XYZ/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "Motion & Code Connect App",
			"nodes": {
				"12:34": {
					"document": {
						"id": "12:34",
						"name": "PrimaryButton",
						"type": "COMPONENT_SET",
						"transitionNodeID": "12:35",
						"transitionDuration": 0.25,
						"transitionEasing": "EASE_IN_AND_OUT",
						"children": []
					},
					"componentSets": {
						"12:34": {
							"id": "12:34",
							"key": "cs_btn",
							"name": "Buttons/PrimaryButton",
							"propertyDefinitions": {
								"Label": {"type": "TEXT", "defaultValue": "Submit"}
							}
						}
					}
				}
			}
		}`))
	})
	mux.HandleFunc("/files/abc123XYZ/dev_resources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"links":[{"id":"r1","name":"Code Connect: PrimaryButton","url":"https://github.com/org/repo/blob/main/PrimaryButton.tsx","file_key":"abc123XYZ","node_id":"12:34"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. fm motion
	outMotion, err := executeCLI(t, "motion", "https://www.figma.com/design/abc123XYZ/App?node-id=12-34",
		"--format", "framer", "--token", "tok123", "--api-base-url", srv.URL)
	if err != nil {
		t.Fatalf("fm motion failed: %v\nOutput: %s", err, outMotion)
	}
	if !strings.Contains(outMotion, "motionTransitions") || !strings.Contains(outMotion, "0.25") {
		t.Fatalf("unexpected fm motion output:\n%s", outMotion)
	}

	// 2. fm code-connect generate & link
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "PrimaryButton.tsx"), []byte(`export function PrimaryButton() { return <button/>; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	outCC, err := executeCLI(t, "code-connect", "generate", "https://www.figma.com/design/abc123XYZ/App?node-id=12-34",
		"--dir", srcDir, "--token", "tok123", "--api-base-url", srv.URL)
	if err != nil {
		t.Fatalf("fm code-connect generate failed: %v\nOutput: %s", err, outCC)
	}
	if !strings.Contains(outCC, "PrimaryButton.figma.ts") {
		t.Fatalf("expected PrimaryButton.figma.ts in output:\n%s", outCC)
	}

	outCCLink, err := executeCLI(t, "code-connect", "link", "https://www.figma.com/design/abc123XYZ/App?node-id=12-34",
		"--dir", srcDir, "--token", "tok123", "--api-base-url", srv.URL)
	if err != nil {
		t.Fatalf("fm code-connect link failed: %v\nOutput: %s", err, outCCLink)
	}
	if !strings.Contains(outCCLink, "Linked 1 Dev Resource") {
		t.Fatalf("expected Linked 1 Dev Resource in output:\n%s", outCCLink)
	}

	// 3. fm mcp over stdin/stdout
	cmd := NewRootCmd()
	inBuf := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n")
	var outBuf bytes.Buffer
	cmd.SetIn(inBuf)
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"mcp", "--token", "tok123", "--api-base-url", srv.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fm mcp failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("unexpected fm mcp initialize output:\n%s", outBuf.String())
	}
}

func TestStitchCLICommand(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/variables/local") {
			_, _ = w.Write([]byte(`{
				"status": 200,
				"error": false,
				"meta": {
					"variables": {
						"v-brand": {
							"id": "v-brand",
							"name": "Colors/BrandPrimary",
							"variableCollectionID": "col-1",
							"resolvedType": "COLOR",
							"valuesByMode": {
								"m-1": {"r": 0.2, "g": 0.4, "b": 0.9, "a": 1.0}
							}
						}
					},
					"variableCollections": {
						"col-1": {
							"id": "col-1",
							"name": "Tokens",
							"defaultModeId": "m-1",
							"modes": [{"modeId": "m-1", "name": "Light"}]
						}
					}
				}
			}`))
			return
		}

		if strings.Contains(r.URL.Path, "/nodes") {
			_, _ = w.Write([]byte(`{
				"name": "DreamsPOS UI Kit",
				"nodes": {
					"1:133": {
						"document": {
							"id": "1:133",
							"name": "Design System",
							"type": "FRAME",
							"children": [
								{
									"id": "c-1",
									"name": "Primary / 500",
									"type": "FRAME",
									"fills": [{"type": "SOLID", "color": {"r": 0.99, "g": 0.62, "b": 0.26, "a": 1.0}}]
								}
							]
						}
					}
				}
			}`))
			return
		}

		if strings.HasPrefix(r.URL.Path, "/v1/files/") {
			_, _ = w.Write([]byte(`{
				"name": "DreamsPOS UI Kit",
				"document": {
					"id": "0:0",
					"name": "Document",
					"type": "DOCUMENT",
					"children": []
				}
			}`))
			return
		}

		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	// 1. fm stitch with URL and node-id
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "DESIGN.md")

	out, err := executeCLI(t, "stitch", "https://www.figma.com/design/abc123XYZ/DreamsPOS?node-id=1:133",
		"-o", outFile, "--token", "tok123", "--api-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm stitch failed: %v\nOutput: %s", err, out)
	}

	if !strings.Contains(out, "Successfully generated Google Stitch DESIGN.md") {
		t.Fatalf("unexpected summary in output:\n%s", out)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read generated DESIGN.md: %v", err)
	}

	strContent := string(content)
	if !strings.HasPrefix(strContent, "---\nversion: alpha\n") {
		t.Fatalf("expected DESIGN.md frontmatter, got:\n%s", strContent[:100])
	}
	if !strings.Contains(strContent, "# DreamsPOS UI Kit Design System Specification") {
		t.Fatalf("expected specification title in body, got:\n%s", strContent)
	}

	// 2. fm tokens -f stitch
	outTokensStitch, err := executeCLI(t, "tokens", "https://www.figma.com/design/abc123XYZ/DreamsPOS",
		"-f", "stitch", "--token", "tok123", "--api-base-url", mockServer.URL)
	if err != nil {
		t.Fatalf("fm tokens -f stitch failed: %v\nOutput: %s", err, outTokensStitch)
	}
	if !strings.Contains(outTokensStitch, "version: alpha") {
		t.Fatalf("expected version: alpha in fm tokens -f stitch, got:\n%s", outTokensStitch[:200])
	}
}
