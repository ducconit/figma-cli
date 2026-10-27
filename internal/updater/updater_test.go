package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   int
	}{
		{"0.0.2", "0.0.1", 1},
		{"v0.0.1", "0.0.2", -1},
		{"1.0.0", "1.0.0", 0},
		{"v1.2.3", "v1.2.3", 0},
		{"2.0.0", "1.99.99", 1},
		{"0.1.0", "0.0.9", 1},
		{"1.0.0-rc1", "1.0.0", 0},
	}

	for _, tt := range tests {
		got := CompareSemver(tt.v1, tt.v2)
		if got != tt.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
		}
	}
}

func TestMatchAsset(t *testing.T) {
	assets := []Asset{
		{Name: "fm-linux-amd64", BrowserDownloadURL: "https://example.com/linux-amd64", Size: 1000},
		{Name: "fm-darwin-arm64", BrowserDownloadURL: "https://example.com/darwin-arm64", Size: 2000},
		{Name: "fm-windows-amd64.exe", BrowserDownloadURL: "https://example.com/windows-amd64.exe", Size: 3000},
		{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt", Size: 100},
	}

	// Linux amd64
	match, err := MatchAsset(assets, "linux", "amd64")
	if err != nil || match.Name != "fm-linux-amd64" {
		t.Fatalf("expected fm-linux-amd64, got %+v (err=%v)", match, err)
	}

	// macOS Apple Silicon (darwin / arm64)
	match, err = MatchAsset(assets, "darwin", "arm64")
	if err != nil || match.Name != "fm-darwin-arm64" {
		t.Fatalf("expected fm-darwin-arm64, got %+v (err=%v)", match, err)
	}

	// Windows amd64
	match, err = MatchAsset(assets, "windows", "amd64")
	if err != nil || match.Name != "fm-windows-amd64.exe" {
		t.Fatalf("expected fm-windows-amd64.exe, got %+v (err=%v)", match, err)
	}

	// Unsupported OS
	if _, err := MatchAsset(assets, "freebsd", "amd64"); err == nil {
		t.Fatal("expected error for unsupported OS")
	}
}

func TestUpdaterWorkflowWithMockServer(t *testing.T) {
	latestRelease := Release{
		TagName: "v0.0.2",
		Name:    "v0.0.2 Release",
		Assets: []Asset{
			{Name: "fm-linux-amd64", BrowserDownloadURL: "", Size: 16},
			{Name: "fm-darwin-arm64", BrowserDownloadURL: "", Size: 16},
			{Name: "fm-windows-amd64.exe", BrowserDownloadURL: "", Size: 16},
		},
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			rel := latestRelease
			for i := range rel.Assets {
				rel.Assets[i].BrowserDownloadURL = "http://" + r.Host + "/download/" + rel.Assets[i].Name
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rel)
		case strings.HasPrefix(r.URL.Path, "/download/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("updated-binary-content-v0.0.2"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	tempDir := t.TempDir()
	fakeExe := filepath.Join(tempDir, "fm")
	if err := os.WriteFile(fakeExe, []byte("old-binary-content-v0.0.1"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. CheckOnly mode
	var out bytes.Buffer
	res, err := Run(context.Background(), UpdateOptions{
		CurrentVersion: "0.0.1",
		TargetOS:       "linux",
		TargetArch:     "amd64",
		TargetExePath:  fakeExe,
		CheckOnly:      true,
		BaseURL:        mockServer.URL,
		Out:            &out,
	})
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if !res.HasUpdate || res.LatestVersion != "0.0.2" {
		t.Fatalf("expected HasUpdate=true, LatestVersion=0.0.2, got %+v", res)
	}
	if !strings.Contains(out.String(), "New version available") {
		t.Fatalf("expected update announcement, got:\n%s", out.String())
	}

	// Verify old binary untouched
	data, _ := os.ReadFile(fakeExe)
	if string(data) != "old-binary-content-v0.0.1" {
		t.Fatalf("expected binary untouched, got: %s", string(data))
	}

	// 2. Perform update
	out.Reset()
	res, err = Run(context.Background(), UpdateOptions{
		CurrentVersion: "0.0.1",
		TargetOS:       "linux",
		TargetArch:     "amd64",
		TargetExePath:  fakeExe,
		BaseURL:        mockServer.URL,
		Out:            &out,
	})
	if err != nil {
		t.Fatalf("update execution failed: %v", err)
	}
	if !res.HasUpdate {
		t.Fatalf("expected HasUpdate=true, got %+v", res)
	}

	// Verify binary was actually replaced
	data, err = os.ReadFile(fakeExe)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "updated-binary-content-v0.0.2" {
		t.Fatalf("expected updated binary content, got: %s", string(data))
	}

	// 3. Re-run when already on latest version -> no update needed
	out.Reset()
	res, err = Run(context.Background(), UpdateOptions{
		CurrentVersion: "0.0.2",
		TargetOS:       "linux",
		TargetArch:     "amd64",
		TargetExePath:  fakeExe,
		BaseURL:        mockServer.URL,
		Out:            &out,
	})
	if err != nil {
		t.Fatalf("already latest check failed: %v", err)
	}
	if res.HasUpdate {
		t.Fatalf("expected HasUpdate=false when on latest, got %+v", res)
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("expected 'already up to date' output, got:\n%s", out.String())
	}

	// 4. Force re-download even on latest version
	out.Reset()
	res, err = Run(context.Background(), UpdateOptions{
		CurrentVersion: "0.0.2",
		TargetOS:       "linux",
		TargetArch:     "amd64",
		TargetExePath:  fakeExe,
		Force:          true,
		BaseURL:        mockServer.URL,
		Out:            &out,
	})
	if err != nil {
		t.Fatalf("force update failed: %v", err)
	}
	if !res.HasUpdate {
		t.Fatalf("expected HasUpdate=true on Force, got %+v", res)
	}
}
