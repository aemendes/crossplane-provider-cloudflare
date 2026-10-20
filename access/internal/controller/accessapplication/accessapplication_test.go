package accessapplication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/meta"

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/accessapplication/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/access/internal/clients/cloudflare"
)

func newApplication(name string) *v1alpha1.AccessApplication {
	a := &v1alpha1.AccessApplication{}
	a.SetName(name)
	a.Spec.ForProvider.AccountID = "acct-1"
	a.Spec.ForProvider.Domain = "app.example.com"
	a.Spec.ForProvider.Name = name
	return a
}

func newSaaSApplication(name string) *v1alpha1.AccessApplication {
	a := &v1alpha1.AccessApplication{}
	a.SetName(name)
	a.Spec.ForProvider.AccountID = "acct-1"
	a.Spec.ForProvider.Type = "saas"
	a.Spec.ForProvider.Name = name
	a.Spec.ForProvider.OIDC = &v1alpha1.AccessApplicationOIDCParameters{
		RedirectURIs: []string{"https://app.example.com/callback"},
		GrantTypes:   []string{"authorization_code"},
		Scopes:       []string{"openid", "email"},
	}
	return a
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
	cr := newApplication("my-app")

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
		if got := r.URL.Query().Get("name"); got != "my-app" {
			t.Fatalf("unexpected name query param: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": []map[string]any{
				{"id": "app-123", "domain": "app.example.com", "name": "my-app", "policies": []any{}},
			},
		})
	}))
	cr := newApplication("my-app")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists {
		t.Fatal("expected ResourceExists=true when an application with an exact name match is found")
	}
	if meta.GetExternalName(cr) != "app-123" {
		t.Fatalf("expected external-name to be adopted as app-123, got %q", meta.GetExternalName(cr))
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
	cr := newApplication("my-app")
	meta.SetExternalName(cr, "app-123")

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
				"id": "app-123", "domain": "app.example.com", "name": "my-app",
				"aud": "aud-tag", "session_duration": "24h", "policies": []any{},
			},
		})
	}))
	cr := newApplication("my-app")
	cr.Spec.ForProvider.SessionDuration = "24h"
	meta.SetExternalName(cr, "app-123")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("expected ResourceExists=true and ResourceUpToDate=true, got %+v", obs)
	}
	if cr.Status.AtProvider.AUD != "aud-tag" {
		t.Fatalf("expected status.atProvider.aud to be populated, got %q", cr.Status.AtProvider.AUD)
	}
}

func TestObserveLateInitializesUnsetSessionDuration(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-123", "domain": "app.example.com", "name": "my-app",
				"session_duration": "24h", "policies": []any{},
			},
		})
	}))
	cr := newApplication("my-app") // SessionDuration left unset
	meta.SetExternalName(cr, "app-123")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceLateInitialized {
		t.Fatal("expected ResourceLateInitialized=true when adopting Cloudflare's default sessionDuration")
	}
	if !obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=true once sessionDuration is adopted")
	}
	if cr.Spec.ForProvider.SessionDuration != "24h" {
		t.Fatalf("expected spec.forProvider.sessionDuration to be adopted as 24h, got %q", cr.Spec.ForProvider.SessionDuration)
	}
}

func TestObserveLateInitializesUnsetHTTPOnlyCookieAttribute(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-123", "domain": "app.example.com", "name": "my-app",
				"http_only_cookie_attribute": false, "policies": []any{},
			},
		})
	}))
	cr := newApplication("my-app") // HTTPOnlyCookieAttribute left unset
	meta.SetExternalName(cr, "app-123")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceLateInitialized {
		t.Fatal("expected ResourceLateInitialized=true when adopting the live httpOnlyCookieAttribute value")
	}
	if !obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=true once httpOnlyCookieAttribute is adopted")
	}
	if cr.Spec.ForProvider.HTTPOnlyCookieAttribute == nil || *cr.Spec.ForProvider.HTTPOnlyCookieAttribute {
		t.Fatalf("expected spec.forProvider.httpOnlyCookieAttribute to be adopted as false, got %+v", cr.Spec.ForProvider.HTTPOnlyCookieAttribute)
	}
}

func TestObserveOutOfDateWhenHTTPOnlyCookieAttributeDiffers(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-123", "domain": "app.example.com", "name": "my-app",
				"http_only_cookie_attribute": true, "policies": []any{},
			},
		})
	}))
	cr := newApplication("my-app")
	want := false
	cr.Spec.ForProvider.HTTPOnlyCookieAttribute = &want
	meta.SetExternalName(cr, "app-123")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=false when httpOnlyCookieAttribute differs from the live value")
	}
}

func TestObserveOutOfDateWhenAdditionalDomainsDiffer(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-123", "domain": "coder.example.com", "name": "coder",
				"self_hosted_domains": []string{"coder.example.com"}, // missing the wildcard
				"policies":            []any{},
			},
		})
	}))
	cr := newApplication("coder")
	cr.Spec.ForProvider.Domain = "coder.example.com"
	cr.Spec.ForProvider.AdditionalDomains = []string{"*.coder.example.com"}
	meta.SetExternalName(cr, "app-123")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=false when additionalDomains differ from the live self_hosted_domains")
	}
}

func TestObserveOutOfDateWhenPolicyIDsDiffer(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-123", "domain": "app.example.com", "name": "my-app",
				"policies": []map[string]any{{"id": "pol-old"}},
			},
		})
	}))
	cr := newApplication("my-app")
	cr.Spec.ForProvider.PolicyIDs = []string{"pol-new"}
	meta.SetExternalName(cr, "app-123")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=false when attached policy IDs differ")
	}
}

func TestCreateSetsExternalName(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["domain"] != "app.example.com" {
			t.Fatalf("unexpected domain: %v", body["domain"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{"id": "app-123", "domain": "app.example.com", "name": "my-app", "policies": []any{}},
		})
	}))
	cr := newApplication("my-app")

	if _, err := e.Create(context.Background(), cr); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.GetExternalName(cr) != "app-123" {
		t.Fatalf("expected external-name to be set to app-123, got %q", meta.GetExternalName(cr))
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
	cr := newApplication("my-app")
	meta.SetExternalName(cr, "app-123")

	if _, err := e.Delete(context.Background(), cr); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestObserveSaaSUpToDateAndPopulatesClientID(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-saas-1", "type": "saas", "name": "my-saas-app", "policies": []any{},
				"saas_app": map[string]any{
					"client_id":             "generated-client-id",
					"redirect_uris":         []string{"https://app.example.com/callback"},
					"grant_types":           []string{"authorization_code"},
					"scopes":                []string{"email", "openid"}, // deliberately reordered
					"access_token_lifetime": "5m",
				},
			},
		})
	}))
	cr := newSaaSApplication("my-saas-app")
	meta.SetExternalName(cr, "app-saas-1")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("expected ResourceExists=true and ResourceUpToDate=true (scopes order shouldn't matter), got %+v", obs)
	}
	if cr.Status.AtProvider.ClientID != "generated-client-id" {
		t.Fatalf("expected status.atProvider.clientId to be populated, got %q", cr.Status.AtProvider.ClientID)
	}
	if !obs.ResourceLateInitialized || cr.Spec.ForProvider.OIDC.AccessTokenLifetime != "5m" {
		t.Fatalf("expected accessTokenLifetime to be late-initialized to 5m, got %+v", cr.Spec.ForProvider.OIDC)
	}
}

func TestObserveSaaSOutOfDateWhenRedirectURIsDiffer(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-saas-1", "type": "saas", "name": "my-saas-app", "policies": []any{},
				"saas_app": map[string]any{
					"client_id":     "generated-client-id",
					"redirect_uris": []string{"https://old.example.com/callback"},
					"grant_types":   []string{"authorization_code"},
					"scopes":        []string{"openid", "email"},
				},
			},
		})
	}))
	cr := newSaaSApplication("my-saas-app")
	meta.SetExternalName(cr, "app-saas-1")

	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("expected ResourceUpToDate=false when redirectUris differ")
	}
}

func TestCreateSaaSApplicationPublishesClientSecret(t *testing.T) {
	e := newTestExternal(t, jsonHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["type"] != "saas" {
			t.Fatalf("unexpected type: %v", body["type"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "messages": []any{},
			"result": map[string]any{
				"id": "app-saas-1", "type": "saas", "name": "my-saas-app", "policies": []any{},
				"saas_app": map[string]any{
					"client_id":     "generated-client-id",
					"client_secret": "one-shot-secret",
					"redirect_uris": []string{"https://app.example.com/callback"},
				},
			},
		})
	}))
	cr := newSaaSApplication("my-saas-app")

	creation, err := e.Create(context.Background(), cr)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.GetExternalName(cr) != "app-saas-1" {
		t.Fatalf("expected external-name to be set to app-saas-1, got %q", meta.GetExternalName(cr))
	}
	if string(creation.ConnectionDetails["clientId"]) != "generated-client-id" {
		t.Fatalf("unexpected clientId: %q", creation.ConnectionDetails["clientId"])
	}
	if string(creation.ConnectionDetails["clientSecret"]) != "one-shot-secret" {
		t.Fatalf("unexpected clientSecret: %q", creation.ConnectionDetails["clientSecret"])
	}
}
