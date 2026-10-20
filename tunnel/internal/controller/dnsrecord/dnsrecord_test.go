package dnsrecord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/meta"

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/dnsrecord/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/tunnel/internal/clients/cloudflare"
)

func newDNSRecord() *v1alpha1.DNSRecord {
	d := &v1alpha1.DNSRecord{}
	d.SetName("my-record")
	d.Spec.ForProvider.ZoneID = "zone-1"
	d.Spec.ForProvider.Name = "app.example.com"
	d.Spec.ForProvider.TunnelID = "tun-123"
	d.Spec.ForProvider.Proxied = true
	return d
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
		if r.URL.Path != "/zones/zone-1/dns_records" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{}, "result": []any{},
		})
	}))
	cr := newDNSRecord()

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
		if got := r.URL.Query().Get("name.exact"); got != "app.example.com" {
			t.Fatalf("unexpected name.exact query param: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": []map[string]any{
				{"id": "rec-1", "name": "app.example.com", "content": "tun-123.cfargotunnel.com", "proxied": true},
			},
		})
	}))
	cr := newDNSRecord()

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists {
		t.Fatal("expected ResourceExists=true when a record with an exact name match is found")
	}
	if meta.GetExternalName(cr) != "rec-1" {
		t.Fatalf("expected external-name to be adopted as rec-1, got %q", meta.GetExternalName(cr))
	}
	if !obs.ResourceLateInitialized {
		t.Fatal("expected ResourceLateInitialized=true so the adopted external-name annotation is persisted")
	}
}

func TestObserveNotFound(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 81044, "message": "not found"}},
		})
	}))
	cr := newDNSRecord()
	meta.SetExternalName(cr, "rec-1")

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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "rec-1", "name": "app.example.com", "content": "tun-123.cfargotunnel.com", "proxied": true,
			},
		})
	}))
	cr := newDNSRecord()
	meta.SetExternalName(cr, "rec-1")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("expected ResourceExists=true and ResourceUpToDate=true, got %+v", obs)
	}
	if cr.Status.AtProvider.Content != "tun-123.cfargotunnel.com" {
		t.Fatalf("expected status.atProvider.content to be populated, got %q", cr.Status.AtProvider.Content)
	}
}

func TestObserveOutOfDateWhenContentDiffers(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "rec-1", "name": "app.example.com", "content": "old-tunnel.cfargotunnel.com", "proxied": true,
			},
		})
	}))
	cr := newDNSRecord()
	meta.SetExternalName(cr, "rec-1")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=false when the CNAME target differs from the current tunnel")
	}
}

func TestCreateSetsExternalName(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["content"] != "tun-123.cfargotunnel.com" {
			t.Fatalf("unexpected content: %v", body["content"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{"id": "rec-1", "name": "app.example.com", "content": "tun-123.cfargotunnel.com", "proxied": true},
		})
	}))
	cr := newDNSRecord()

	if _, err := e.Create(context.Background(), cr); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.GetExternalName(cr) != "rec-1" {
		t.Fatalf("expected external-name to be set to rec-1, got %q", meta.GetExternalName(cr))
	}
}

func TestDeleteIsNoopWhenAlreadyGone(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 81044, "message": "not found"}},
		})
	}))
	cr := newDNSRecord()
	meta.SetExternalName(cr, "rec-1")

	if _, err := e.Delete(context.Background(), cr); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
