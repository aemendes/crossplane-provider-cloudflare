package policy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/meta"

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/policy/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/access/internal/clients/cloudflare"
)

func newPolicy(name string) *v1alpha1.Policy {
	p := &v1alpha1.Policy{}
	p.SetName(name)
	p.Spec.ForProvider.AccountID = "acct-1"
	p.Spec.ForProvider.Name = name
	p.Spec.ForProvider.Decision = "allow"
	p.Spec.ForProvider.Include = []v1alpha1.AccessRule{{EmailDomain: "example.com"}}
	return p
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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{}, "result": []any{},
		})
	}))
	cr := newPolicy("my-policy")

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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": []map[string]any{
				{"id": "pol-1", "name": "my-policy", "decision": "allow",
					"include": []map[string]any{{"email_domain": map[string]any{"domain": "example.com"}}}},
			},
		})
	}))
	cr := newPolicy("my-policy")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists {
		t.Fatal("expected ResourceExists=true when a policy with an exact name match is found")
	}
	if meta.GetExternalName(cr) != "pol-1" {
		t.Fatalf("expected external-name to be adopted as pol-1, got %q", meta.GetExternalName(cr))
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
			"errors": []map[string]any{{"code": 1000, "message": "not found"}},
		})
	}))
	cr := newPolicy("my-policy")
	meta.SetExternalName(cr, "pol-1")

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
				"id": "pol-1", "name": "my-policy", "decision": "allow",
				"include": []map[string]any{{"email_domain": map[string]any{"domain": "example.com"}}},
			},
		})
	}))
	cr := newPolicy("my-policy")
	meta.SetExternalName(cr, "pol-1")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("expected ResourceExists=true and ResourceUpToDate=true, got %+v", obs)
	}
	if cr.Status.AtProvider.ID != "pol-1" {
		t.Fatalf("expected status.atProvider.id to be populated, got %q", cr.Status.AtProvider.ID)
	}
}

func TestObserveOutOfDateWhenIncludeDiffers(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "pol-1", "name": "my-policy", "decision": "allow",
				"include": []map[string]any{{"everyone": map[string]any{}}},
			},
		})
	}))
	cr := newPolicy("my-policy")
	meta.SetExternalName(cr, "pol-1")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=false when include rules differ")
	}
}

func TestCreateSetsExternalName(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "pol-1", "name": "my-policy", "decision": "allow",
				"include": []map[string]any{{"email_domain": map[string]any{"domain": "example.com"}}},
			},
		})
	}))
	cr := newPolicy("my-policy")

	if _, err := e.Create(context.Background(), cr); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.GetExternalName(cr) != "pol-1" {
		t.Fatalf("expected external-name to be set to pol-1, got %q", meta.GetExternalName(cr))
	}
}

// TestUpdateSendsAnyValidServiceTokenNotEveryone is a regression test for a
// real incident: toClientRules() used to omit AnyValidServiceToken when
// converting a Policy's spec.forProvider.include to Cloudflare API params,
// so an Update against a live any_valid_service_token/nonIdentity policy
// silently rewrote it to "everyone" — briefly letting anonymous traffic
// through a production app gated by that policy, since toAccessRuleParam's
// fallback for an all-zero-value rule was Everyone (the single most
// permissive rule Cloudflare has).
func TestUpdateSendsAnyValidServiceTokenNotEveryone(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		include := body["include"].([]any)
		if len(include) != 1 {
			t.Fatalf("expected 1 include rule, got %d", len(include))
		}
		rule := include[0].(map[string]any)
		if _, ok := rule["any_valid_service_token"]; !ok {
			t.Fatalf("expected any_valid_service_token to be sent, got %v (this is the exact bug that briefly let anonymous traffic through production)", rule)
		}
		if _, ok := rule["everyone"]; ok {
			t.Fatalf("must never send everyone for a rule declared as anyValidValidServiceToken, got %v", rule)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "pol-1", "name": "cf-service-auth", "decision": "non_identity",
				"include": []map[string]any{{"any_valid_service_token": map[string]any{}}},
			},
		})
	}))
	cr := newPolicy("cf-service-auth")
	cr.Spec.ForProvider.Decision = "nonIdentity"
	cr.Spec.ForProvider.Include = []v1alpha1.AccessRule{{AnyValidServiceToken: true}}
	meta.SetExternalName(cr, "pol-1")

	if _, err := e.Update(context.Background(), cr); err != nil {
		t.Fatalf("Update: %v", err)
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
	cr := newPolicy("my-policy")
	meta.SetExternalName(cr, "pol-1")

	if _, err := e.Delete(context.Background(), cr); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
