package v1alpha1

import (
	"context"
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	tunnelv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/tunnel/v1alpha1"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := tunnelv1alpha1.SchemeBuilder.AddToScheme(s); err != nil {
		t.Fatalf("add tunnel scheme: %v", err)
	}
	if err := SchemeBuilder.AddToScheme(s); err != nil {
		t.Fatalf("add dnsrecord scheme: %v", err)
	}
	return s
}

func TestResolveReferencesByRef(t *testing.T) {
	tunnel := &tunnelv1alpha1.Tunnel{ObjectMeta: metav1.ObjectMeta{Name: "my-tunnel"}}
	tunnel.Status.AtProvider.ID = "tun-123"

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(tunnel).Build()

	rec := &DNSRecord{}
	rec.Spec.ForProvider.TunnelIDRef = &xpv1.Reference{Name: "my-tunnel"}

	if err := rec.ResolveReferences(context.Background(), c); err != nil {
		t.Fatalf("ResolveReferences: %v", err)
	}
	if rec.Spec.ForProvider.TunnelID != "tun-123" {
		t.Fatalf("expected tunnelId resolved to tun-123, got %q", rec.Spec.ForProvider.TunnelID)
	}
}

func TestResolveReferencesDoesNotOverwriteExistingValue(t *testing.T) {
	tunnel := &tunnelv1alpha1.Tunnel{ObjectMeta: metav1.ObjectMeta{Name: "my-tunnel"}}
	tunnel.Status.AtProvider.ID = "tun-123"

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(tunnel).Build()

	rec := &DNSRecord{}
	rec.Spec.ForProvider.TunnelID = "already-set"
	rec.Spec.ForProvider.TunnelIDRef = &xpv1.Reference{Name: "my-tunnel"}

	if err := rec.ResolveReferences(context.Background(), c); err != nil {
		t.Fatalf("ResolveReferences: %v", err)
	}
	if rec.Spec.ForProvider.TunnelID != "already-set" {
		t.Fatalf("expected an already-set tunnelId to be left alone, got %q", rec.Spec.ForProvider.TunnelID)
	}
}
