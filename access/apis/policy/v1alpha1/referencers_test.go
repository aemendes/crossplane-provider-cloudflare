package v1alpha1

import (
	"context"
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	servicetokenv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/servicetoken/v1alpha1"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := servicetokenv1alpha1.SchemeBuilder.AddToScheme(s); err != nil {
		t.Fatalf("add servicetoken scheme: %v", err)
	}
	if err := SchemeBuilder.AddToScheme(s); err != nil {
		t.Fatalf("add policy scheme: %v", err)
	}
	return s
}

func TestResolveServiceTokenRefInEveryRuleList(t *testing.T) {
	tok := &servicetokenv1alpha1.ServiceToken{ObjectMeta: metav1.ObjectMeta{Name: "radio"}}
	tok.Status.AtProvider.ID = "tok-1"
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(tok).Build()

	p := &Policy{}
	p.Spec.ForProvider.Include = []AccessRule{{Email: "a@b.c"}, {ServiceTokenRef: &xpv1.Reference{Name: "radio"}}}
	p.Spec.ForProvider.Require = []AccessRule{{ServiceTokenRef: &xpv1.Reference{Name: "radio"}}}

	if err := p.ResolveReferences(context.Background(), c); err != nil {
		t.Fatalf("ResolveReferences: %v", err)
	}
	if got := p.Spec.ForProvider.Include[1].ServiceTokenID; got != "tok-1" {
		t.Fatalf("include[1].serviceTokenId = %q, want tok-1", got)
	}
	if got := p.Spec.ForProvider.Require[0].ServiceTokenID; got != "tok-1" {
		t.Fatalf("require[0].serviceTokenId = %q, want tok-1", got)
	}
	if p.Spec.ForProvider.Include[0].ServiceTokenID != "" {
		t.Fatalf("a rule without a ref must stay untouched, got %+v", p.Spec.ForProvider.Include[0])
	}
}

func TestResolveServiceTokenRefFailsWhileTokenMissing(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	p := &Policy{}
	p.Spec.ForProvider.Include = []AccessRule{{ServiceTokenRef: &xpv1.Reference{Name: "radio"}}}
	if err := p.ResolveReferences(context.Background(), c); err == nil {
		t.Fatalf("expected an error while the ServiceToken does not exist")
	}
	if p.Spec.ForProvider.Include[0].ServiceTokenID != "" {
		t.Fatalf("serviceTokenId must stay empty (an empty rule is refused, never sent)")
	}
}
