package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/meta"

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/tunnel/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/tunnel/internal/clients/cloudflare"
)

func newTunnel(name string) *v1alpha1.Tunnel {
	t := &v1alpha1.Tunnel{}
	t.SetName(name)
	t.Spec.ForProvider.AccountID = "acct-1"
	return t
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

func TestObserveNoExternalNameAndNoMatchByName(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/acct-1/cfd_tunnel" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{}, "result": []any{},
		})
	}))
	cr := newTunnel("my-tunnel")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceExists {
		t.Fatal("expected ResourceExists=false when no external-name annotation is set and no name match is found")
	}
	if meta.GetExternalName(cr) != "" {
		t.Fatalf("expected external-name to stay unset, got %q", meta.GetExternalName(cr))
	}
}

func TestObserveAdoptsByNameWhenExternalNameMissing(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts/acct-1/cfd_tunnel":
			if got := r.URL.Query().Get("name"); got != "my-tunnel" {
				t.Fatalf("unexpected name query param: %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{}, "messages": []any{},
				"result": []map[string]any{{"id": "tun-123", "name": "my-tunnel", "status": "healthy"}},
			})
		case "/accounts/acct-1/cfd_tunnel/tun-123/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{}, "messages": []any{}, "result": "conn-token",
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	cr := newTunnel("my-tunnel")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists {
		t.Fatal("expected ResourceExists=true when a tunnel with an exact name match is found")
	}
	if meta.GetExternalName(cr) != "tun-123" {
		t.Fatalf("expected external-name to be adopted as tun-123, got %q", meta.GetExternalName(cr))
	}
	if !obs.ResourceLateInitialized {
		t.Fatal("expected ResourceLateInitialized=true so the adopted external-name annotation is persisted")
	}
	if string(obs.ConnectionDetails["tunnelToken"]) != "conn-token" {
		t.Fatalf("expected connector token to be published, got %+v", obs.ConnectionDetails)
	}
}

func TestObserveNotFound(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "not found"}},
		})
	}))
	cr := newTunnel("my-tunnel")
	meta.SetExternalName(cr, "tun-123")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceExists {
		t.Fatal("expected ResourceExists=false for a 404 from Cloudflare")
	}
}

func TestObserveUpToDate(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts/acct-1/cfd_tunnel/tun-123":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{}, "messages": []any{},
				"result": map[string]any{"id": "tun-123", "name": "my-tunnel", "status": "healthy"},
			})
		case "/accounts/acct-1/cfd_tunnel/tun-123/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{}, "messages": []any{}, "result": "conn-token",
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	cr := newTunnel("my-tunnel")
	meta.SetExternalName(cr, "tun-123")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists {
		t.Fatal("expected ResourceExists=true")
	}
	if !obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=true when Cloudflare's name matches spec")
	}
	if cr.Status.AtProvider.ID != "tun-123" {
		t.Fatalf("expected status.atProvider.id to be populated, got %q", cr.Status.AtProvider.ID)
	}
	if cr.Status.AtProvider.Status != "healthy" {
		t.Fatalf("expected status.atProvider.status to be populated, got %q", cr.Status.AtProvider.Status)
	}
}

func TestCreateSetsExternalNameAndConnectionDetails(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/acct-1/cfd_tunnel":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{}, "messages": []any{},
				"result": map[string]any{"id": "tun-123", "name": "my-tunnel"},
			})
		case r.URL.Path == "/accounts/acct-1/cfd_tunnel/tun-123/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{}, "messages": []any{}, "result": "conn-token",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	cr := newTunnel("my-tunnel")

	creation, err := e.Create(context.Background(), cr)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.GetExternalName(cr) != "tun-123" {
		t.Fatalf("expected external-name to be set to tun-123, got %q", meta.GetExternalName(cr))
	}
	if string(creation.ConnectionDetails["tunnelToken"]) != "conn-token" {
		t.Fatalf("unexpected connection details: %v", creation.ConnectionDetails)
	}
}

func TestDeleteIsNoopWhenAlreadyGone(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "not found"}},
		})
	}))
	cr := newTunnel("my-tunnel")
	meta.SetExternalName(cr, "tun-123")

	if _, err := e.Delete(context.Background(), cr); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
