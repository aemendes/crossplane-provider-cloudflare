package tunnelconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/tunnelconfig/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/tunnel/internal/clients/cloudflare"
)

func newTunnelConfig() *v1alpha1.TunnelConfig {
	tc := &v1alpha1.TunnelConfig{}
	tc.SetName("my-tunnel-config")
	tc.Spec.ForProvider.AccountID = "acct-1"
	tc.Spec.ForProvider.TunnelID = "tun-123"
	tc.Spec.ForProvider.Ingress = []v1alpha1.IngressRule{
		{Hostname: "app.example.com", Service: "http://localhost:8080"},
		{Service: "http_status:404"},
	}
	return tc
}

func newTestExternal(t *testing.T, handler http.HandlerFunc) *external {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &external{client: cloudflare.NewClientWithBaseURL("test-token", srv.URL)}
}

func jsonHandler(t *testing.T, fn func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fn(w, r)
	}
}

func TestObserveResourceGoneWhenTunnelDeleted(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "tunnel not found"}},
		})
	}))
	cr := newTunnelConfig()

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceExists {
		t.Fatal("expected ResourceExists=false when the tunnel is gone")
	}
}

func TestObserveUpToDate(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"tunnel_id": "tun-123", "version": 3,
				"config": map[string]any{"ingress": []map[string]any{
					{"hostname": "app.example.com", "service": "http://localhost:8080"},
					{"service": "http_status:404"},
				}},
			},
		})
	}))
	cr := newTunnelConfig()

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("expected ResourceExists=true and ResourceUpToDate=true, got %+v", obs)
	}
	if cr.Status.AtProvider.Version != 3 {
		t.Fatalf("expected status.atProvider.version to be populated, got %d", cr.Status.AtProvider.Version)
	}
}

func TestObserveOutOfDateWhenIngressDiffers(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"tunnel_id": "tun-123", "version": 1,
				"config": map[string]any{"ingress": []map[string]any{
					{"service": "http_status:404"},
				}},
			},
		})
	}))
	cr := newTunnelConfig()

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=false when ingress rules differ")
	}
}

func TestCreateSetsIngress(t *testing.T) {
	var saw bool
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/accounts/acct-1/cfd_tunnel/tun-123/configurations" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		saw = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{"tunnel_id": "tun-123", "version": 1, "config": map[string]any{"ingress": []any{}}},
		})
	}))
	cr := newTunnelConfig()

	if _, err := e.Create(context.Background(), cr); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !saw {
		t.Fatal("expected a PUT to the configurations endpoint")
	}
}

func TestDeleteResetsToCatchAllOnly(t *testing.T) {
	var gotIngress []any
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotIngress = body["config"].(map[string]any)["ingress"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{"tunnel_id": "tun-123", "version": 2, "config": map[string]any{"ingress": gotIngress}},
		})
	}))
	cr := newTunnelConfig()

	if _, err := e.Delete(context.Background(), cr); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(gotIngress) != 1 {
		t.Fatalf("expected ingress reset to a single catch-all rule, got %+v", gotIngress)
	}
}

func TestDeleteIsNoopWhenTunnelAlreadyGone(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "tunnel not found"}},
		})
	}))
	cr := newTunnelConfig()

	if _, err := e.Delete(context.Background(), cr); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
