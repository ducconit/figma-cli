package updater

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultGitHubRepo = "ducconit/figma-cli"
	DefaultTimeout    = 30 * time.Second
)

// Release represents a GitHub release.
type Release struct {
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// Asset represents a downloadable binary in a GitHub release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// UpdateOptions configures the updater operation.
type UpdateOptions struct {
	CurrentVersion string
	TargetVersion  string // optional specific tag, e.g. "v0.0.2"
	TargetOS       string // defaults to runtime.GOOS
	TargetArch     string // defaults to runtime.GOARCH
	TargetExePath  string // defaults to os.Executable()
	CheckOnly      bool
	Force          bool
	BaseURL        string // custom endpoint for tests
	Proxy          string // proxy URL or "none"
	Out            io.Writer
}

// UpdateResult provides the summary of the update check or execution.
type UpdateResult struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	HasUpdate      bool   `json:"hasUpdate"`
	BinaryPath     string `json:"binaryPath,omitempty"`
	AssetURL       string `json:"assetUrl,omitempty"`
	Message        string `json:"message"`
}

// Run executes the update check and optional download/replacement.
func Run(ctx context.Context, opts UpdateOptions) (*UpdateResult, error) {
	if opts.TargetOS == "" {
		opts.TargetOS = runtime.GOOS
	}
	if opts.TargetArch == "" {
		opts.TargetArch = runtime.GOARCH
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}

	currentVer := strings.TrimSpace(opts.CurrentVersion)
	if currentVer == "" {
		currentVer = "0.0.1"
	}

	httpClient, err := buildHTTPClient(opts.Proxy)
	if err != nil {
		return nil, fmt.Errorf("create http client: %w", err)
	}

	var release *Release
	if opts.TargetVersion != "" {
		tag := opts.TargetVersion
		if !strings.HasPrefix(tag, "v") && !strings.Contains(tag, "/") {
			tag = "v" + tag
		}
		release, err = fetchReleaseByTag(ctx, httpClient, opts.BaseURL, tag)
	} else {
		release, err = fetchLatestRelease(ctx, httpClient, opts.BaseURL)
	}
	if err != nil {
		return nil, err
	}

	latestVer := strings.TrimPrefix(release.TagName, "v")
	hasUpdate := isNewerVersion(currentVer, latestVer) || opts.Force

	result := &UpdateResult{
		CurrentVersion: currentVer,
		LatestVersion:  latestVer,
		HasUpdate:      hasUpdate,
	}

	if !hasUpdate {
		result.Message = fmt.Sprintf("fm is already up to date: v%s (latest: %s)", currentVer, release.TagName)
		fmt.Fprintln(opts.Out, result.Message)
		return result, nil
	}

	asset, err := MatchAsset(release.Assets, opts.TargetOS, opts.TargetArch)
	if err != nil {
		return nil, fmt.Errorf("find compatible binary for %s/%s in release %s: %w", opts.TargetOS, opts.TargetArch, release.TagName, err)
	}
	result.AssetURL = asset.BrowserDownloadURL

	if opts.CheckOnly {
		result.Message = fmt.Sprintf("New version available: %s (current: v%s). Run 'fm update' to upgrade.", release.TagName, currentVer)
		fmt.Fprintln(opts.Out, result.Message)
		return result, nil
	}

	targetExe := opts.TargetExePath
	if targetExe == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve current executable: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			targetExe = resolved
		} else {
			targetExe = exe
		}
	}
	result.BinaryPath = targetExe

	fmt.Fprintf(opts.Out, "Updating fm from v%s to %s (%s/%s)...\n", currentVer, release.TagName, opts.TargetOS, opts.TargetArch)
	fmt.Fprintf(opts.Out, "Downloading %s (%d bytes)...\n", asset.Name, asset.Size)

	if err := downloadAndReplace(ctx, httpClient, asset.BrowserDownloadURL, targetExe, opts.TargetOS); err != nil {
		return nil, fmt.Errorf("apply update: %w", err)
	}

	result.Message = fmt.Sprintf("Successfully updated fm to %s at %s", release.TagName, targetExe)
	fmt.Fprintln(opts.Out, result.Message)
	return result, nil
}

// MatchAsset finds the most appropriate binary asset for the specified OS and Architecture.
func MatchAsset(assets []Asset, targetOS, targetArch string) (*Asset, error) {
	normOS := strings.ToLower(targetOS)
	normArch := strings.ToLower(targetArch)

	// Build prioritized patterns
	var patterns []string
	switch normOS {
	case "linux":
		patterns = []string{
			fmt.Sprintf("fm-linux-%s", normArch),
			fmt.Sprintf("fm_linux_%s", normArch),
		}
	case "darwin", "macos":
		if normArch == "arm64" {
			patterns = []string{
				"fm-darwin-arm64",
				"fm-macos-arm64",
				"fm_darwin_arm64",
				"fm_macos_arm64",
			}
		} else {
			patterns = []string{
				"fm-darwin-amd64",
				"fm-macos-amd64",
				"fm_darwin_amd64",
				"fm_macos_amd64",
			}
		}
	case "windows":
		patterns = []string{
			fmt.Sprintf("fm-windows-%s.exe", normArch),
			fmt.Sprintf("fm_windows_%s.exe", normArch),
			"fm.exe",
		}
	default:
		return nil, fmt.Errorf("unsupported operating system: %s", targetOS)
	}

	for _, pat := range patterns {
		for i := range assets {
			if strings.EqualFold(assets[i].Name, pat) {
				return &assets[i], nil
			}
		}
	}

	// Secondary check: look for assets that contain both OS and Arch
	for i := range assets {
		name := strings.ToLower(assets[i].Name)
		if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".txt") {
			continue
		}
		osMatch := strings.Contains(name, normOS) || (normOS == "darwin" && strings.Contains(name, "macos"))
		archMatch := strings.Contains(name, normArch)
		if osMatch && archMatch {
			return &assets[i], nil
		}
	}

	return nil, fmt.Errorf("no asset matching patterns: %v", patterns)
}

func fetchLatestRelease(ctx context.Context, client *http.Client, baseURL string) (*Release, error) {
	apiURL := "https://api.github.com/repos/" + DefaultGitHubRepo + "/releases/latest"
	if baseURL != "" {
		apiURL = strings.TrimSuffix(baseURL, "/") + "/repos/" + DefaultGitHubRepo + "/releases/latest"
	}
	return fetchRelease(ctx, client, apiURL)
}

func fetchReleaseByTag(ctx context.Context, client *http.Client, baseURL, tag string) (*Release, error) {
	apiURL := "https://api.github.com/repos/" + DefaultGitHubRepo + "/releases/tags/" + tag
	if baseURL != "" {
		apiURL = strings.TrimSuffix(baseURL, "/") + "/repos/" + DefaultGitHubRepo + "/releases/tags/" + tag
	}
	return fetchRelease(ctx, client, apiURL)
}

func fetchRelease(ctx context.Context, client *http.Client, apiURL string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create release request: %w", err)
	}
	req.Header.Set("User-Agent", "figma-cli")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("github api returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release json: %w", err)
	}
	return &rel, nil
}

func downloadAndReplace(ctx context.Context, client *http.Client, downloadURL, targetExe, targetOS string) error {
	dir := filepath.Dir(targetExe)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure binary directory exists: %w", err)
	}

	// Create unique temporary file in the same directory to guarantee atomic rename
	randomHex := make([]byte, 4)
	_, _ = rand.Read(randomHex)
	tmpPath := targetExe + ".tmp-" + hex.EncodeToString(randomHex)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "figma-cli")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create temporary file %s: %w", tmpPath, err)
	}

	_, copyErr := io.Copy(tmpFile, resp.Body)
	closeErr := tmpFile.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		if copyErr != nil {
			return fmt.Errorf("write temporary binary: %w", copyErr)
		}
		return fmt.Errorf("close temporary binary: %w", closeErr)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod binary: %w", err)
	}

	return replaceBinary(tmpPath, targetExe, targetOS)
}

func replaceBinary(tmpPath, targetExe, targetOS string) error {
	if strings.EqualFold(targetOS, "windows") {
		// Windows locks running binaries, but allows renaming them
		oldPath := targetExe + ".old"
		_ = os.Remove(oldPath) // remove previous backup if exists
		if err := os.Rename(targetExe, oldPath); err != nil && !os.IsNotExist(err) {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("backup running windows binary: %w", err)
		}
		if err := os.Rename(tmpPath, targetExe); err != nil {
			// Try to restore old binary
			_ = os.Rename(oldPath, targetExe)
			_ = os.Remove(tmpPath)
			return fmt.Errorf("replace windows binary: %w", err)
		}
		_ = os.Remove(oldPath)
		return nil
	}

	// Unix (Linux/macOS) supports atomic replacement of active inode via rename
	if err := os.Rename(tmpPath, targetExe); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename binary: %w", err)
	}
	return nil
}

// CompareSemver compares two semantic versions (e.g. "0.0.2" and "0.0.1").
// Returns 1 if v1 > v2, -1 if v1 < v2, and 0 if v1 == v2.
func CompareSemver(v1, v2 string) int {
	clean1 := strings.TrimPrefix(strings.TrimSpace(v1), "v")
	clean2 := strings.TrimPrefix(strings.TrimSpace(v2), "v")

	if clean1 == clean2 {
		return 0
	}

	parts1 := strings.Split(clean1, ".")
	parts2 := strings.Split(clean2, ".")

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int64
		if i < len(parts1) {
			numPart := strings.Split(parts1[i], "-")[0]
			n1, _ = strconv.ParseInt(numPart, 10, 64)
		}
		if i < len(parts2) {
			numPart := strings.Split(parts2[i], "-")[0]
			n2, _ = strconv.ParseInt(numPart, 10, 64)
		}

		if n1 > n2 {
			return 1
		}
		if n1 < n2 {
			return -1
		}
	}

	return 0
}

func isNewerVersion(current, latest string) bool {
	if current == "dev" || current == "unknown" {
		return true
	}
	return CompareSemver(latest, current) > 0
}

func buildHTTPClient(proxyURL string) (*http.Client, error) {
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression: false,
	}

	if proxyURL != "" && !strings.EqualFold(proxyURL, "none") {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy url %q: %w", proxyURL, err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	} else if strings.EqualFold(proxyURL, "none") {
		transport.Proxy = nil
	}

	return &http.Client{
		Transport: transport,
		Timeout:   DefaultTimeout,
	}, nil
}
