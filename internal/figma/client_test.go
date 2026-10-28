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

func TestClientVariablesAndDevResources(t *testing.T) {
	mux := http.NewServeMux()

	// 1. Local Variables endpoint
	mux.HandleFunc("/files/test-vars/variables/local", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": 200,
			"error": false,
			"meta": {
				"variables": {
					"var-1": {
						"id": "var-1",
						"name": "colors/primary",
						"key": "key-1",
						"variableCollectionId": "col-1",
						"resolvedType": "COLOR",
						"valuesByMode": {
							"m1": {"r": 0.2, "g": 0.4, "b": 0.8, "a": 1.0}
						}
					}
				},
				"variableCollections": {
					"col-1": {
						"id": "col-1",
						"name": "Theme",
						"key": "colkey-1",
						"defaultModeId": "m1",
						"modes": [{"modeId": "m1", "name": "Light"}]
					}
				}
			}
		}`))
	})

	// 2. Dev Resources endpoint
	mux.HandleFunc("/files/test-dev/dev_resources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			nodeIDs := r.URL.Query().Get("node_ids")
			if nodeIDs == "12:34" {
				_, _ = w.Write([]byte(`{
					"links": [
						{"id": "link-1", "name": "Storybook", "url": "https://storybook.example.com", "file_key": "test-dev", "node_id": "12:34"}
					]
				}`))
			} else {
				_, _ = w.Write([]byte(`{
					"links": [
						{"id": "link-1", "name": "Storybook", "url": "https://storybook.example.com", "file_key": "test-dev", "node_id": "12:34"},
						{"id": "link-2", "name": "PR 42", "url": "https://github.com/org/repo/pull/42", "file_key": "test-dev", "node_id": "56:78"}
					]
				}`))
			}
		case http.MethodPost:
			var reqPayload struct {
				DevResources []DevResourceInput `json:"dev_resources"`
			}
			if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if len(reqPayload.DevResources) == 0 || reqPayload.DevResources[0].NodeID != "12:34" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{
				"links": [
					{"id": "link-new", "name": "` + reqPayload.DevResources[0].Name + `", "url": "` + reqPayload.DevResources[0].URL + `", "file_key": "test-dev", "node_id": "12:34"}
				]
			}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// 3. Delete Dev Resource endpoint
	mux.HandleFunc("/files/test-dev/dev_resources/link-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := NewClient(config.AuthConfig{APIKey: "pat_test"}, "none")
	if err != nil {
		t.Fatal(err)
	}
	client.SetBaseURL(srv.URL)

	// Test GetLocalVariables
	vars, err := client.GetLocalVariables(context.Background(), "test-vars")
	if err != nil {
		t.Fatalf("GetLocalVariables failed: %v", err)
	}
	if len(vars.Variables) != 1 || vars.Variables["var-1"].Name != "colors/primary" {
		t.Fatalf("unexpected variables: %+v", vars)
	}
	if len(vars.VariableCollections) != 1 || vars.VariableCollections["col-1"].Name != "Theme" {
		t.Fatalf("unexpected collections: %+v", vars)
	}

	// Test GetDevResources (all vs specific node)
	allLinks, err := client.GetDevResources(context.Background(), "test-dev", nil)
	if err != nil || len(allLinks) != 2 {
		t.Fatalf("GetDevResources(all) failed: %+v (err=%v)", allLinks, err)
	}
	nodeLinks, err := client.GetDevResources(context.Background(), "test-dev", []string{"12-34"})
	if err != nil || len(nodeLinks) != 1 || nodeLinks[0].ID != "link-1" {
		t.Fatalf("GetDevResources(node) failed: %+v (err=%v)", nodeLinks, err)
	}

	// Test CreateDevResources
	created, err := client.CreateDevResources(context.Background(), "test-dev", []DevResourceInput{
		{Name: "New PR", URL: "https://github.com/org/repo/pull/99", NodeID: "12-34"},
	})
	if err != nil || len(created) != 1 || created[0].ID != "link-new" {
		t.Fatalf("CreateDevResources failed: %+v (err=%v)", created, err)
	}

	// Test DeleteDevResource
	if err := client.DeleteDevResource(context.Background(), "test-dev", "link-1"); err != nil {
		t.Fatalf("DeleteDevResource failed: %v", err)
	}
}
