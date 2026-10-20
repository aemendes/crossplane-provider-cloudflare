package v1alpha1

import (
	"context"
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	policyv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/access/apis/policy/v1alpha1"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := policyv1alpha1.SchemeBuilder.AddToScheme(s); err != nil {
		t.Fatalf("add policy scheme: %v", err)
	}
	if err := SchemeBuilder.AddToScheme(s); err != nil {
		t.Fatalf("add application scheme: %v", err)
	}
	return s
}

func TestResolveReferencesByRef(t *testing.T) {
	p1 := &policyv1alpha1.Policy{ObjectMeta: metav1.ObjectMeta{Name: "policy-one"}}
	p1.Status.AtProvider.ID = "pol-1"
	p2 := &policyv1alpha1.Policy{ObjectMeta: metav1.ObjectMeta{Name: "policy-two"}}
	p2.Status.AtProvider.ID = "pol-2"

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(p1, p2).Build()

	app := &AccessApplication{}
	app.Spec.ForProvider.PolicyIDRefs = []xpv1.Reference{{Name: "policy-one"}, {Name: "policy-two"}}

	if err := app.ResolveReferences(context.Background(), c); err != nil {
		t.Fatalf("ResolveReferences: %v", err)
	}
	if len(app.Spec.ForProvider.PolicyIDs) != 2 || app.Spec.ForProvider.PolicyIDs[0] != "pol-1" || app.Spec.ForProvider.PolicyIDs[1] != "pol-2" {
		t.Fatalf("expected policyIds resolved to [pol-1 pol-2] in order, got %v", app.Spec.ForProvider.PolicyIDs)
	}
}

func TestResolveReferencesDoesNotOverwriteExistingValue(t *testing.T) {
	p1 := &policyv1alpha1.Policy{ObjectMeta: metav1.ObjectMeta{Name: "policy-one"}}
	p1.Status.AtProvider.ID = "pol-1"

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(p1).Build()

	app := &AccessApplication{}
	app.Spec.ForProvider.PolicyIDs = []string{"already-set"}
	app.Spec.ForProvider.PolicyIDRefs = []xpv1.Reference{{Name: "policy-one"}}

	if err := app.ResolveReferences(context.Background(), c); err != nil {
		t.Fatalf("ResolveReferences: %v", err)
	}
	if len(app.Spec.ForProvider.PolicyIDs) != 1 || app.Spec.ForProvider.PolicyIDs[0] != "already-set" {
		t.Fatalf("expected an already-set policyIds to be left alone, got %v", app.Spec.ForProvider.PolicyIDs)
	}
}
