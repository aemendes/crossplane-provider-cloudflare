// Package tunnelconfig manages the lifecycle of TunnelConfig managed resources.
package tunnelconfig

import (
	"context"
	"reflect"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	crossplanecontroller "github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/tunnelconfig/v1alpha1"
	apisv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/tunnel/internal/clients/cloudflare"
)

const (
	errNotTunnelConfig = "managed resource is not a TunnelConfig custom resource"
	errTrackPCUsage    = "cannot track ProviderConfig usage"
	errGetPC           = "cannot get ProviderConfig"
	errGetCreds        = "cannot get credentials"

	errObserve = "cannot observe Cloudflare tunnel configuration"
	errSet     = "cannot set Cloudflare tunnel configuration"
)

// resetIngress is the ingress configuration a TunnelConfig is reset to on
// delete: just the mandatory catch-all, since Cloudflare has no endpoint to
// remove a tunnel's configuration outright.
var resetIngress = []cloudflare.IngressRule{{Service: "http_status:404"}}

// Setup adds a controller that reconciles TunnelConfig managed resources.
func Setup(mgr ctrl.Manager, o crossplanecontroller.Options) error {
	name := managed.ControllerName(v1alpha1.TunnelConfigKind)

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind(v1alpha1.TunnelConfigKind)),
		managed.WithExternalConnecter(&connector{
			kube:        mgr.GetClient(),
			usage:       resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClientFn: cloudflare.NewClient,
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithPollInterval(o.PollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(controller.Options{MaxConcurrentReconciles: o.MaxConcurrentReconciles}).
		For(&v1alpha1.TunnelConfig{}).
		Complete(r)
}

// connector produces an ExternalClient by reading the Cloudflare API token out
// of the ProviderConfig referenced by the TunnelConfig being reconciled.
type connector struct {
	kube        client.Client
	usage       resource.Tracker
	newClientFn func(apiToken string) *cloudflare.Client
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.TunnelConfig)
	if !ok {
		return nil, errors.New(errNotTunnelConfig)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	data, err := resource.CommonCredentialExtractor(ctx, pc.Spec.Credentials.Source, c.kube, pc.Spec.Credentials.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	return &external{client: c.newClientFn(string(data))}, nil
}

// external observes, creates, updates and deletes a Cloudflare tunnel's
// ingress configuration on behalf of a TunnelConfig managed resource.
type external struct {
	client *cloudflare.Client
}

// Compile-time checks that connector/external satisfy the crossplane-runtime
// interfaces managed.NewReconciler requires. A missing method here surfaces
// only as a runtime panic on provider startup.
var (
	_ managed.ExternalConnecter = &connector{}
	_ managed.ExternalClient    = &external{}
)

func (e *external) Disconnect(_ context.Context) error {
	return nil
}

func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.TunnelConfig)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotTunnelConfig)
	}

	cfg, err := e.client.GetTunnelIngress(ctx, cr.Spec.ForProvider.AccountID, cr.Spec.ForProvider.TunnelID)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			// Either the tunnel itself is gone, or no configuration has ever
			// been set for it — in both cases there is nothing of ours to
			// observe, so report it as not existing rather than erroring
			// forever (which would leave Delete unreachable if the parent
			// Tunnel was removed first).
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
	}

	cr.SetConditions(xpv1.Available())
	cr.Status.AtProvider = v1alpha1.TunnelConfigObservation{Version: cfg.Version}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: rulesEqual(cfg.Rules, toClientRules(cr.Spec.ForProvider.Ingress)),
	}, nil
}

func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.TunnelConfig)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotTunnelConfig)
	}

	cr.SetConditions(xpv1.Creating())

	if err := e.client.SetTunnelIngress(ctx, cr.Spec.ForProvider.AccountID, cr.Spec.ForProvider.TunnelID, toClientRules(cr.Spec.ForProvider.Ingress)); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errSet)
	}

	return managed.ExternalCreation{}, nil
}

func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.TunnelConfig)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotTunnelConfig)
	}

	if err := e.client.SetTunnelIngress(ctx, cr.Spec.ForProvider.AccountID, cr.Spec.ForProvider.TunnelID, toClientRules(cr.Spec.ForProvider.Ingress)); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errSet)
	}

	return managed.ExternalUpdate{}, nil
}

// Delete resets the tunnel's ingress to just the mandatory catch-all rule.
// Cloudflare has no endpoint to remove a tunnel's configuration outright, so
// this is the closest equivalent to "this resource no longer manages ingress".
func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.TunnelConfig)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotTunnelConfig)
	}

	cr.SetConditions(xpv1.Deleting())

	if err := e.client.SetTunnelIngress(ctx, cr.Spec.ForProvider.AccountID, cr.Spec.ForProvider.TunnelID, resetIngress); err != nil {
		if cloudflare.IsNotFound(err) {
			// The parent tunnel is already gone — nothing left to reset.
			return managed.ExternalDelete{}, nil
		}
		return managed.ExternalDelete{}, errors.Wrap(err, errSet)
	}

	return managed.ExternalDelete{}, nil
}

func toClientRules(rules []v1alpha1.IngressRule) []cloudflare.IngressRule {
	out := make([]cloudflare.IngressRule, len(rules))
	for i, r := range rules {
		out[i] = cloudflare.IngressRule{Hostname: r.Hostname, Service: r.Service, Path: r.Path, NoTLSVerify: r.NoTLSVerify}
	}
	return out
}

func rulesEqual(a, b []cloudflare.IngressRule) bool {
	if len(a) != len(b) {
		return false
	}
	return reflect.DeepEqual(a, b)
}
