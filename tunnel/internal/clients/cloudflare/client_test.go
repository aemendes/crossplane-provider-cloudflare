package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	cf "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/option"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	// WithMaxRetries(0): the SDK retries 429s and 5xxs with exponential
	// backoff by default, which would make TestGetTunnelRateLimited take
	// tens of seconds for no reason in a test that already knows the answer.
	return &Client{sdk: cf.NewClient(
		option.WithAPIToken("test-api-token"),
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
	)}
}

func TestCreateTunnel(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/accounts/acct-1/cfd_tunnel" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-api-token" {
			t.Fatalf("unexpected Authorization header: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["name"] != "my-tunnel" {
			t.Fatalf("unexpected name: %v", body["name"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{"id": "tun-123", "name": "my-tunnel", "status": "inactive"},
		})
	})

	tun, err := c.CreateTunnel(context.Background(), "acct-1", "my-tunnel")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if tun.ID != "tun-123" {
		t.Fatalf("unexpected tunnel ID: %q", tun.ID)
	}
}

func TestGetTunnelNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "tunnel not found"}},
		})
	})

	_, err := c.GetTunnel(context.Background(), "acct-1", "tun-123")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound(err) to be true, got %v", err)
	}
}

func TestGetTunnelSoftDeletedIsNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "tun-123", "name": "my-tunnel", "status": "down",
				"deleted_at": "2026-07-21T09:17:16.695823Z",
			},
		})
	})

	_, err := c.GetTunnel(context.Background(), "acct-1", "tun-123")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound(err) to be true (soft-deleted tunnel), got %v", err)
	}
}

func TestGetTunnelRateLimited(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 10000, "message": "rate limited"}},
		})
	})

	_, err := c.GetTunnel(context.Background(), "acct-1", "tun-123")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsRateLimited(err) {
		t.Fatalf("expected IsRateLimited(err) to be true, got %v", err)
	}
}

func TestListTunnelsByExactName(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/acct-1/cfd_tunnel" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "my-tunnel" {
			t.Fatalf("unexpected name query param: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": []map[string]any{
				{"id": "tun-123", "name": "my-tunnel"},
			},
			"result_info": map[string]any{"count": 1, "page": 1, "per_page": 400, "total_count": 1},
		})
	})

	tunnels, err := c.ListTunnelsByExactName(context.Background(), "acct-1", "my-tunnel")
	if err != nil {
		t.Fatalf("ListTunnelsByExactName: %v", err)
	}
	if len(tunnels) != 1 || tunnels[0].ID != "tun-123" {
		t.Fatalf("unexpected result: %+v", tunnels)
	}
}

func TestDeleteTunnelIsNoopWhenAlreadyGone(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 1000, "message": "not found"}},
		})
	})

	if err := c.DeleteTunnel(context.Background(), "acct-1", "tun-123"); err != nil {
		t.Fatalf("DeleteTunnel: %v", err)
	}
}

func TestGetTunnelToken(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/acct-1/cfd_tunnel/tun-123/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": "opaque-connector-token",
		})
	})

	token, err := c.GetTunnelToken(context.Background(), "acct-1", "tun-123")
	if err != nil {
		t.Fatalf("GetTunnelToken: %v", err)
	}
	if token != "opaque-connector-token" {
		t.Fatalf("unexpected token: %q", token)
	}
}

func TestSetAndGetTunnelIngress(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/acct-1/cfd_tunnel/tun-123/configurations" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodPut:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			config := body["config"].(map[string]any)
			ingress := config["ingress"].([]any)
			if len(ingress) != 2 {
				t.Fatalf("expected 2 ingress rules, got %d", len(ingress))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{}, "messages": []any{},
				"result": map[string]any{
					"tunnel_id": "tun-123", "version": 2,
					"config": map[string]any{"ingress": ingress},
				},
			})
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{}, "messages": []any{},
				"result": map[string]any{
					"tunnel_id": "tun-123", "version": 2,
					"config": map[string]any{"ingress": []map[string]any{
						{"hostname": "app.example.com", "service": "http://localhost:8080"},
						{"service": "http_status:404"},
					}},
				},
			})
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	})

	rules := []IngressRule{
		{Hostname: "app.example.com", Service: "http://localhost:8080"},
		{Service: "http_status:404"},
	}
	if err := c.SetTunnelIngress(context.Background(), "acct-1", "tun-123", rules); err != nil {
		t.Fatalf("SetTunnelIngress: %v", err)
	}

	cfg, err := c.GetTunnelIngress(context.Background(), "acct-1", "tun-123")
	if err != nil {
		t.Fatalf("GetTunnelIngress: %v", err)
	}
	if cfg.Version != 2 {
		t.Fatalf("unexpected version: %d", cfg.Version)
	}
	if len(cfg.Rules) != 2 || cfg.Rules[0].Hostname != "app.example.com" || cfg.Rules[1].Service != "http_status:404" {
		t.Fatalf("unexpected rules: %+v", cfg.Rules)
	}
}

func TestSetTunnelIngressSendsNoTLSVerify(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		config := body["config"].(map[string]any)
		ingress := config["ingress"].([]any)
		rule0 := ingress[0].(map[string]any)
		originRequest, ok := rule0["originRequest"].(map[string]any)
		if !ok || originRequest["noTLSVerify"] != true {
			t.Fatalf("expected originRequest.noTLSVerify=true on rule 0, got %v", rule0)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{"tunnel_id": "tun-123", "version": 1, "config": map[string]any{"ingress": ingress}},
		})
	})

	rules := []IngressRule{
		{Hostname: "internal.example.com", Service: "https://localhost:8443", NoTLSVerify: true},
		{Service: "http_status:404"},
	}
	if err := c.SetTunnelIngress(context.Background(), "acct-1", "tun-123", rules); err != nil {
		t.Fatalf("SetTunnelIngress: %v", err)
	}
}

func TestGetTunnelIngressReadsNoTLSVerify(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"tunnel_id": "tun-123", "version": 1,
				"config": map[string]any{"ingress": []map[string]any{
					{"hostname": "internal.example.com", "service": "https://localhost:8443", "originRequest": map[string]any{"noTLSVerify": true}},
					{"service": "http_status:404"},
				}},
			},
		})
	})

	cfg, err := c.GetTunnelIngress(context.Background(), "acct-1", "tun-123")
	if err != nil {
		t.Fatalf("GetTunnelIngress: %v", err)
	}
	if len(cfg.Rules) != 2 || !cfg.Rules[0].NoTLSVerify {
		t.Fatalf("expected rule 0 to decode NoTLSVerify=true, got %+v", cfg.Rules)
	}
}

func TestCreateDNSRecord(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones/zone-1/dns_records" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["type"] != "CNAME" {
			t.Fatalf("unexpected type: %v", body["type"])
		}
		if body["content"] != "tun-123.cfargotunnel.com" {
			t.Fatalf("unexpected content: %v", body["content"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "rec-1", "name": "app.example.com", "content": "tun-123.cfargotunnel.com", "proxied": true,
			},
		})
	})

	rec, err := c.CreateDNSRecord(context.Background(), "zone-1", "app.example.com", "tun-123.cfargotunnel.com", true)
	if err != nil {
		t.Fatalf("CreateDNSRecord: %v", err)
	}
	if rec.ID != "rec-1" || !rec.Proxied {
		t.Fatalf("unexpected result: %+v", rec)
	}
}

func TestListDNSRecordsByExactName(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones/zone-1/dns_records" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("name.exact"); got != "app.example.com" {
			t.Fatalf("unexpected name.exact query param: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": []map[string]any{
				{"id": "rec-1", "name": "app.example.com", "content": "tun-123.cfargotunnel.com", "proxied": true},
			},
			"result_info": map[string]any{"count": 1, "page": 1, "per_page": 100, "total_count": 1},
		})
	})

	records, err := c.ListDNSRecordsByExactName(context.Background(), "zone-1", "app.example.com")
	if err != nil {
		t.Fatalf("ListDNSRecordsByExactName: %v", err)
	}
	if len(records) != 1 || records[0].ID != "rec-1" {
		t.Fatalf("unexpected result: %+v", records)
	}
}

func TestDeleteDNSRecordIsNoopWhenAlreadyGone(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false, "messages": []any{},
			"errors": []map[string]any{{"code": 81044, "message": "record does not exist"}},
		})
	})

	if err := c.DeleteDNSRecord(context.Background(), "zone-1", "rec-1"); err != nil {
		t.Fatalf("DeleteDNSRecord: %v", err)
	}
}
