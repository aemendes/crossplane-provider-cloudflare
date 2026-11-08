package servicetoken

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/servicetoken/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/access/internal/clients/cloudflare"
)

var now = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// fakeAPI is a minimal Cloudflare service token API. It records every
// request as "METHOD /path-suffix" (suffix after /access/service_tokens).
type fakeAPI struct {
	mu        sync.Mutex
	calls     []string
	tokens    []map[string]any // returned by list
	token     map[string]any   // returned by get/update/refresh
	notFound  bool
	newSecret string
}

func (f *fakeAPI) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_, suffix, _ := strings.Cut(r.URL.Path, "/access/service_tokens")
		f.calls = append(f.calls, r.Method+" "+suffix)
		w.Header().Set("Content-Type", "application/json")
		write := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": result})
		}
		switch {
		case r.Method == http.MethodGet && suffix == "":
			write(f.tokens)
		case r.Method == http.MethodPost && suffix == "":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			write(map[string]any{"id": "tok-new", "name": body["name"], "client_id": "cid-new.access", "client_secret": "sec-new", "duration": "8760h"})
		case strings.HasSuffix(suffix, "/rotate"):
			write(map[string]any{"id": "tok-1", "name": "t", "client_id": "cid-1.access", "client_secret": f.newSecret, "duration": "8760h"})
		case f.notFound:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 404, "message": "not found"}}, "messages": []any{}, "result": nil})
		default: // get, put, refresh, delete
			write(f.token)
		}
	}
}

func (f *fakeAPI) called(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

func token(expires time.Time) map[string]any {
	return map[string]any{"id": "tok-1", "name": "t", "client_id": "cid-1.access", "duration": "8760h", "expires_at": expires.Format(time.RFC3339)}
}

func newCR(externalName string) *v1alpha1.ServiceToken {
	cr := &v1alpha1.ServiceToken{}
	cr.SetName("t")
	cr.Spec.ForProvider.AccountID = "acct-1"
	cr.Spec.ForProvider.Name = "t"
	cr.Spec.WriteConnectionSecretToReference = &xpv1.SecretReference{Namespace: "ns", Name: "t-conn"}
	if externalName != "" {
		meta.SetExternalName(cr, externalName)
	}
	return cr
}

func secret(data map[string]string) client.Object {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "t-conn"}, Data: map[string][]byte{}}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	return s
}

func newExternal(t *testing.T, api *fakeAPI, objs ...client.Object) *external {
	t.Helper()
	srv := httptest.NewServer(api.handler(t))
	t.Cleanup(srv.Close)
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	return &external{
		client:  cloudflare.NewClientWithBaseURL("test-token", srv.URL),
		secrets: fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build(),
		now:     func() time.Time { return now },
	}
}

var goodSecret = map[string]string{keyClientID: "cid-1.access", keyClientSecret: "sec-1"}

func TestObserveNoExternalNameAndNoMatch(t *testing.T) {
	e := newExternal(t, &fakeAPI{tokens: []map[string]any{}})
	obs, err := e.Observe(context.Background(), newCR(""))
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceExists {
		t.Fatal("expected ResourceExists=false")
	}
}

func TestObserveAdoptsByNameAndWantsRotation(t *testing.T) {
	e := newExternal(t, &fakeAPI{tokens: []map[string]any{token(now.AddDate(1, 0, 0))}})
	cr := newCR("")
	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists || !obs.ResourceLateInitialized {
		t.Fatalf("expected an adopted, late-initialized resource, got %+v", obs)
	}
	if meta.GetExternalName(cr) != "tok-1" {
		t.Fatalf("external-name = %q, want tok-1", meta.GetExternalName(cr))
	}
	if obs.ResourceUpToDate {
		t.Fatal("an adopted token has no known secret: expected ResourceUpToDate=false so Update rotates it")
	}
}

func TestObserveUpToDate(t *testing.T) {
	e := newExternal(t, &fakeAPI{token: token(now.AddDate(1, 0, 0))}, secret(goodSecret))
	cr := newCR("tok-1")
	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceExists || !obs.ResourceUpToDate {
		t.Fatalf("expected an existing, up-to-date token, got %+v", obs)
	}
	if cr.Spec.ForProvider.Duration != "8760h" || !obs.ResourceLateInitialized {
		t.Fatalf("expected duration late-initialized to 8760h, got %q (lateInit=%v)", cr.Spec.ForProvider.Duration, obs.ResourceLateInitialized)
	}
	if cr.Status.AtProvider.ClientID != "cid-1.access" || cr.Status.AtProvider.ExpiresAt == nil {
		t.Fatalf("unexpected status %+v", cr.Status.AtProvider)
	}
}

func TestObserveNotFound(t *testing.T) {
	e := newExternal(t, &fakeAPI{notFound: true})
	obs, err := e.Observe(context.Background(), newCR("tok-gone"))
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceExists {
		t.Fatal("expected ResourceExists=false for a 404")
	}
}

func TestObserveSecretFromAnotherToken(t *testing.T) {
	e := newExternal(t, &fakeAPI{token: token(now.AddDate(1, 0, 0))}, secret(map[string]string{keyClientID: "other", keyClientSecret: "x"}))
	obs, err := e.Observe(context.Background(), newCR("tok-1"))
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("a connection secret holding another token's clientId must trigger a rotation")
	}
}

func TestObserveNoConnectionSecretRefNeverRotates(t *testing.T) {
	e := newExternal(t, &fakeAPI{token: token(now.AddDate(1, 0, 0))})
	cr := newCR("tok-1")
	cr.Spec.WriteConnectionSecretToReference = nil
	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceUpToDate {
		t.Fatal("without writeConnectionSecretToRef there is nothing to heal: expected up to date")
	}
}

func TestCreatePublishesSecret(t *testing.T) {
	e := newExternal(t, &fakeAPI{})
	cr := newCR("")
	c, err := e.Create(context.Background(), cr)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta.GetExternalName(cr) != "tok-new" {
		t.Fatalf("external-name = %q, want tok-new", meta.GetExternalName(cr))
	}
	if string(c.ConnectionDetails[keyClientID]) != "cid-new.access" || string(c.ConnectionDetails[keyClientSecret]) != "sec-new" {
		t.Fatalf("unexpected connection details %v", c.ConnectionDetails)
	}
}

func TestUpdateRotatesWhenSecretMissing(t *testing.T) {
	api := &fakeAPI{token: token(now.AddDate(1, 0, 0)), newSecret: "sec-rotated"}
	e := newExternal(t, api)
	cr := newCR("tok-1")
	cr.Spec.ForProvider.Duration = "8760h"
	u, err := e.Update(context.Background(), cr)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !api.called("POST /tok-1/rotate") {
		t.Fatalf("expected a rotate call, got %v", api.calls)
	}
	if string(u.ConnectionDetails[keyClientSecret]) != "sec-rotated" || string(u.ConnectionDetails[keyClientID]) != "cid-1.access" {
		t.Fatalf("unexpected connection details %v", u.ConnectionDetails)
	}
	if api.called("PUT /tok-1") || api.called("POST /tok-1/refresh") {
		t.Fatalf("only a rotation was needed, got %v", api.calls)
	}
}

func TestRenewWhenCloseToExpiry(t *testing.T) {
	api := &fakeAPI{token: token(now.Add(10 * 24 * time.Hour))}
	e := newExternal(t, api, secret(goodSecret))
	cr := newCR("tok-1")
	cr.Spec.ForProvider.Duration = "8760h"
	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if obs.ResourceUpToDate {
		t.Fatal("a token expiring in 10 days (default renewBefore 30 days) should not be up to date")
	}
	u, err := e.Update(context.Background(), cr)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !api.called("POST /tok-1/refresh") {
		t.Fatalf("expected a refresh call, got %v", api.calls)
	}
	if api.called("POST /tok-1/rotate") || len(u.ConnectionDetails) != 0 {
		t.Fatalf("a refresh keeps the secret: expected no rotation, got %v", api.calls)
	}
}

func TestRenewDisabled(t *testing.T) {
	e := newExternal(t, &fakeAPI{token: token(now.Add(24 * time.Hour))}, secret(goodSecret))
	cr := newCR("tok-1")
	cr.Spec.ForProvider.Duration = "8760h"
	cr.Spec.ForProvider.RenewBefore = "0s"
	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !obs.ResourceUpToDate {
		t.Fatal("renewBefore 0s disables renewal")
	}
}

func TestRenewBeforeCappedForShortDurations(t *testing.T) {
	e := &external{now: func() time.Time { return now }}
	cr := newCR("tok-1")
	// 24h token, renewBefore defaults to 30 days: capped to 8h.
	fresh := &cloudflare.ServiceToken{Duration: "24h", ExpiresAt: now.Add(20 * time.Hour)}
	if renew, _ := e.needsRenew(cr, fresh); renew {
		t.Fatal("20h left of a 24h token is above the 8h cap: must not renew every poll")
	}
	old := &cloudflare.ServiceToken{Duration: "24h", ExpiresAt: now.Add(2 * time.Hour)}
	if renew, _ := e.needsRenew(cr, old); !renew {
		t.Fatal("2h left of a 24h token is below the 8h cap: must renew")
	}
}

func TestUpdateChangesName(t *testing.T) {
	api := &fakeAPI{token: token(now.AddDate(1, 0, 0))}
	e := newExternal(t, api, secret(goodSecret))
	cr := newCR("tok-1")
	cr.Spec.ForProvider.Name = "renamed"
	if _, err := e.Update(context.Background(), cr); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !api.called("PUT /tok-1") {
		t.Fatalf("expected a PUT for the rename, got %v", api.calls)
	}
}

func TestDurationsEqual(t *testing.T) {
	cases := []struct {
		observed, desired string
		want              bool
	}{
		{"8760h", "8760h0m0s", true},
		{"8760h", "", true},
		{"8760h", "24h", false},
		{"weird", "weird", true},
	}
	for _, c := range cases {
		if got := durationsEqual(c.observed, c.desired); got != c.want {
			t.Errorf("durationsEqual(%q, %q) = %v, want %v", c.observed, c.desired, got, c.want)
		}
	}
}
