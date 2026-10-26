package figma

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ducconit/figma-cli/internal/config"
)

func TestParseFigmaURLAndResolveTarget(t *testing.T) {
	parts, err := ParseFigmaURL("https://www.figma.com/design/abc123XYZ/My-App?node-id=12-34&t=xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parts.FileKey != "abc123XYZ" || parts.NodeID != "12:34" {
		t.Fatalf("unexpected URLParts: %+v", parts)
	}

	// Unsupported /proto/ URL
	if _, err := ParseFigmaURL("https://www.figma.com/proto/abc123XYZ/My-App"); err == nil {
		t.Fatal("expected error for /proto/ URL")
	}

	// Flag override over URL (including comma-separated multi-node IDs)
	fk, nid, err := ResolveTarget("https://www.figma.com/file/urlKey/Design?node-id=1-2", "flagKey", "99-88, 77-66")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fk != "flagKey" || nid != "99:88,77:66" {
		t.Fatalf("expected flagKey and 99:88,77:66, got %s %s", fk, nid)
	}
}

func TestConcurrentRenderURLBatchingAndDeduplication(t *testing.T) {
	var batchRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batchRequests.Add(1)
		ids := strings.Split(r.URL.Query().Get("ids"), ",")
		imagesMap := make(map[string]string, len(ids))
		for _, id := range ids {
			imagesMap[id] = "https://cdn.example.com/" + id + ".svg"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"images": imagesMap})
	}))
	defer srv.Close()

	client, err := NewClient(config.AuthConfig{APIKey: "figd_test"}, "none")
	if err != nil {
		t.Fatal(err)
	}
	client.SetBaseURL(srv.URL)

	// 120 unique IDs + 30 duplicates -> should deduplicate to 120 and split into 3 concurrent batches of 50, 50, 20
	var inputIDs []string
	for i := 1; i <= 120; i++ {
		inputIDs = append(inputIDs, fmt.Sprintf("10-%d", i))
		if i <= 30 {
			inputIDs = append(inputIDs, fmt.Sprintf("10:%d", i))
		}
	}

	urls, err := client.GetNodeRenderURLs(context.Background(), "fileKey123", inputIDs, "svg", RenderOptions{
		SVGOptions: DefaultSVGOptions(),
	})
	if err != nil {
		t.Fatalf("GetNodeRenderURLs failed: %v", err)
	}
	if len(urls) != 120 {
		t.Fatalf("expected 120 resolved URLs, got %d", len(urls))
	}
	if got := batchRequests.Load(); got != 3 {
		t.Fatalf("expected 3 concurrent batch requests for 120 unique IDs, got %d", got)
	}
}

func TestClientAuthRateLimitAndSecretRedaction(t *testing.T) {
	secretPAT := "figd_super_secret_token_9999"

	mux := http.NewServeMux()
	mux.HandleFunc("/files/testkey", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Figma-Token"); got != secretPAT {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Mirroring header X-Figma-Token: " + secretPAT))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Test File","document":{"id":"0:0","type":"DOCUMENT"}}`))
	})
	mux.HandleFunc("/files/ratelimited", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "45")
		w.Header().Set("X-Figma-Rate-Limit-Type", "low")
		w.Header().Set("X-Figma-Plan-Tier", "starter")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"err":"Rate limit"}`))
	})
	mux.HandleFunc("/files/forbidden", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Proxy blocked token " + secretPAT))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := NewClient(config.AuthConfig{APIKey: secretPAT}, "none")
	if err != nil {
		t.Fatal(err)
	}
	client.SetBaseURL(srv.URL)

	// 1. Successful fetch
	data, size, err := client.GetRawFile(context.Background(), "testkey", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data["name"] != "Test File" || size <= 0 {
		t.Fatalf("unexpected response: %+v (size=%d)", data, size)
	}

	// 2. Rate limit 429 diagnostics
	_, _, err = client.GetRawFile(context.Background(), "ratelimited", 0)
	if err == nil {
		t.Fatal("expected 429 error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Retry after 45 seconds") || !strings.Contains(msg, "Viewer or Collaborator") || !strings.Contains(msg, "starter plan") {
		t.Fatalf("expected rich 429 diagnostics, got: %s", msg)
	}

	// 3. Forbidden 403 + Secret Redaction
	_, _, err = client.GetRawFile(context.Background(), "forbidden", 0)
	if err == nil {
		t.Fatal("expected 403 error")
	}
	if strings.Contains(err.Error(), secretPAT) {
		t.Fatalf("SECURITY FAILURE: secret token leaked in error message: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("expected [REDACTED] placeholder in error message: %s", err.Error())
	}
}

func TestClientOAuthGetRawNodeImageFillsAndDownloadToFile(t *testing.T) {
	if got := SanitizeNodeIDForFile("12:34;56:78"); got != "12_34_56_78" {
		t.Fatalf("expected 12_34_56_78, got %q", got)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/files/k1/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth_tok_123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("depth") != "2" || r.URL.Query().Get("ids") != "10:20" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"NodeTest","nodes":{"10:20":{"document":{"id":"10:20","type":"FRAME"}}}}`))
	})
	mux.HandleFunc("/files/k1/images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":{"images":{"ref1":"https://cdn.example.com/1.png"}}}`))
	})
	mux.HandleFunc("/asset.svg", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<svg viewBox="0 0 16 16"></svg>`))
	})
	mux.HandleFunc("/missing.svg", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Invalid proxy URL returns error
	if _, err := NewClient(config.AuthConfig{APIKey: "k"}, "://bad-proxy"); err == nil {
		t.Fatal("expected error for invalid proxy URL")
	}

	client, err := NewClient(config.AuthConfig{OAuthToken: "oauth_tok_123", UseOAuth: true}, "none")
	if err != nil {
		t.Fatal(err)
	}
	client.SetBaseURL(srv.URL)

	nodeData, _, err := client.GetRawNode(context.Background(), "k1", "10-20", 2)
	if err != nil || nodeData["name"] != "NodeTest" {
		t.Fatalf("GetRawNode failed: %+v (err=%v)", nodeData, err)
	}

	fills, err := client.GetImageFillURLs(context.Background(), "k1")
	if err != nil || fills["ref1"] != "https://cdn.example.com/1.png" {
		t.Fatalf("GetImageFillURLs failed: %+v (err=%v)", fills, err)
	}

	dest := t.TempDir() + "/sub/asset.svg"
	if err := client.DownloadToFile(context.Background(), srv.URL+"/asset.svg", dest); err != nil {
		t.Fatalf("DownloadToFile failed: %v", err)
	}
	if err := client.DownloadToFile(context.Background(), srv.URL+"/missing.svg", dest); err == nil {
		t.Fatal("expected error for 404 DownloadToFile")
	}
}
