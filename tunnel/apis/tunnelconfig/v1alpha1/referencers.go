package v1alpha1

import (
	"context"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"github.com/crossplane/crossplane-runtime/pkg/reference"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tunnelv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/tunnel/v1alpha1"
)

// ResolveReferences of this TunnelConfig. Detected and called automatically by
// crossplane-runtime's default APISimpleReferenceResolver — see
// managed.NewAPISimpleReferenceResolver's duck-typed interface check.
func (mg *TunnelConfig) ResolveReferences(ctx context.Context, c client.Reader) error {
	r := reference.NewAPIResolver(c, mg)

	tunnel, err := r.Resolve(ctx, reference.ResolutionRequest{
		CurrentValue: mg.Spec.ForProvider.TunnelID,
		Reference:    mg.Spec.ForProvider.TunnelIDRef,
		Selector:     mg.Spec.ForProvider.TunnelIDSelector,
		To:           reference.To{Managed: &tunnelv1alpha1.Tunnel{}, List: &tunnelv1alpha1.TunnelList{}},
		Extract:      tunnelv1alpha1.TunnelID(),
	})
	if err != nil {
		return errors.Wrap(err, "spec.forProvider.tunnelId")
	}
	mg.Spec.ForProvider.TunnelID = tunnel.ResolvedValue
	mg.Spec.ForProvider.TunnelIDRef = tunnel.ResolvedReference

	return nil
}
