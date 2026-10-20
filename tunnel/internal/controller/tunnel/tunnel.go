// Package tunnel manages the lifecycle of Tunnel managed resources.
package tunnel

import (
	"context"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	crossplanecontroller "github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/tunnel/v1alpha1"
	apisv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/tunnel/internal/clients/cloudflare"
)

const (
	errNotTunnel    = "managed resource is not a Tunnel custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errObserve = "cannot observe Cloudflare tunnel"
	errCreate  = "cannot create Cloudflare tunnel"
	errUpdate  = "cannot update Cloudflare tunnel"
	errDelete  = "cannot delete Cloudflare tunnel"
	errToken   = "cannot fetch Cloudflare tunnel token" // #nosec G101 -- error message, not a credential
)

// Setup adds a controller that reconciles Tunnel managed resources.
func Setup(mgr ctrl.Manager, o crossplanecontroller.Options) error {
	name := managed.ControllerName(v1alpha1.TunnelKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind(v1alpha1.TunnelKind)),
		managed.WithExternalConnecter(&connector{
			kube:        mgr.GetClient(),
			usage:       resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClientFn: cloudflare.NewClient,
		}),
		// Disable the reconciler's default initializer, which otherwise sets
		// external-name to the managed resource's own metadata.name before the
		// first Observe ever runs. That would make Observe's adopt-by-name
		// branch below (which only fires when external-name is empty)
		// unreachable — Cloudflare rejects a Create against a name that
		// already exists, so without this a lost external-name annotation
		// would leave the tunnel permanently unmanageable.
		managed.WithInitializers(),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...),
		managed.WithPollInterval(o.PollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(controller.Options{MaxConcurrentReconciles: o.MaxConcurrentReconciles}).
		For(&v1alpha1.Tunnel{}).
		Complete(r)
}

// connector produces an ExternalClient by reading the Cloudflare API token out
// of the ProviderConfig referenced by the Tunnel being reconciled.
type connector struct {
	kube        client.Client
	usage       resource.Tracker
	newClientFn func(apiToken string) *cloudflare.Client
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Tunnel)
	if !ok {
		return nil, errors.New(errNotTunnel)
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

// external observes, creates, updates and deletes Cloudflare tunnels on behalf of a Tunnel managed resource.
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
	cr, ok := mg.(*v1alpha1.Tunnel)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotTunnel)
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		// The external-name annotation is unset. Before declaring the resource
		// doesn't exist, check whether a tunnel with this exact name already
		// exists: Cloudflare rejects duplicate names outright, so if a prior
		// Create succeeded but the pod died before persisting the annotation,
		// a blind retry would fail forever against the now-taken name.
		found, err := e.findByExactName(ctx, cr.Spec.ForProvider.AccountID, desiredName(cr))
		if err != nil {
			return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
		}
		if found == nil {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		meta.SetExternalName(cr, found.ID)
		obs, err := e.observed(ctx, cr, found)
		if err != nil {
			return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
		}
		// Observe's annotation changes aren't persisted unless we say so —
		// flag this one as a late-init so the reconciler writes the adopted
		// external-name back to the API server instead of re-discovering it
		// via a ListTunnelsByExactName call on every subsequent poll.
		obs.ResourceLateInitialized = true
		return obs, nil
	}

	t, err := e.client.GetTunnel(ctx, cr.Spec.ForProvider.AccountID, id)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
	}

	obs, err := e.observed(ctx, cr, t)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
	}
	return obs, nil
}

// observed records the given external state on cr and returns the resulting
// ExternalObservation, including a freshly re-fetched connector token. t is
// assumed to exist (a successful Get, or a Create/List match).
func (e *external) observed(ctx context.Context, cr *v1alpha1.Tunnel, t *cloudflare.Tunnel) (managed.ExternalObservation, error) {
	cr.SetConditions(xpv1.Available())
	cr.Status.AtProvider = v1alpha1.TunnelObservation{
		ID:        t.ID,
		Status:    t.Status,
		CreatedAt: t.CreatedAt,
	}

	conn, err := e.connectionDetails(ctx, cr.Spec.ForProvider.AccountID, t.ID)
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  t.Name == desiredName(cr),
		ConnectionDetails: conn,
	}, nil
}

// connectionDetails re-fetches the tunnel's connector token. Unlike a
// one-shot secret, Cloudflare lets this be retrieved at any time, so
// republishing it on every Observe lets the connection secret self-heal if
// it's ever lost or the Secret is deleted out-of-band.
func (e *external) connectionDetails(ctx context.Context, accountID, tunnelID string) (managed.ConnectionDetails, error) {
	token, err := e.client.GetTunnelToken(ctx, accountID, tunnelID)
	if err != nil {
		return nil, errors.Wrap(err, errToken)
	}
	return managed.ConnectionDetails{
		"tunnelId":    []byte(tunnelID),
		"tunnelToken": []byte(token),
	}, nil
}

// findByExactName searches Cloudflare for a tunnel with exactly the given
// name, returning nil if none is found.
func (e *external) findByExactName(ctx context.Context, accountID, name string) (*cloudflare.Tunnel, error) {
	candidates, err := e.client.ListTunnelsByExactName(ctx, accountID, name)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		if candidates[i].Name == name {
			return &candidates[i], nil
		}
	}
	return nil, nil
}

func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Tunnel)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotTunnel)
	}

	cr.SetConditions(xpv1.Creating())

	t, err := e.client.CreateTunnel(ctx, cr.Spec.ForProvider.AccountID, desiredName(cr))
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreate)
	}

	meta.SetExternalName(cr, t.ID)

	conn, err := e.connectionDetails(ctx, cr.Spec.ForProvider.AccountID, t.ID)
	if err != nil {
		// The tunnel itself was created successfully — the token is
		// re-fetchable, so the next Observe will populate the connection
		// secret even if this attempt failed.
		return managed.ExternalCreation{}, errors.Wrap(err, errCreate)
	}

	return managed.ExternalCreation{ConnectionDetails: conn}, nil
}

func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Tunnel)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotTunnel)
	}

	if _, err := e.client.RenameTunnel(ctx, cr.Spec.ForProvider.AccountID, meta.GetExternalName(cr), desiredName(cr)); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdate)
	}

	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.Tunnel)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotTunnel)
	}

	cr.SetConditions(xpv1.Deleting())

	if err := e.client.DeleteTunnel(ctx, cr.Spec.ForProvider.AccountID, meta.GetExternalName(cr)); err != nil {
		return managed.ExternalDelete{}, errors.Wrap(err, errDelete)
	}

	return managed.ExternalDelete{}, nil
}

// desiredName is the tunnel name to reconcile towards: the user-supplied name
// if set, falling back to the managed resource's own name.
func desiredName(cr *v1alpha1.Tunnel) string {
	if cr.Spec.ForProvider.Name != "" {
		return cr.Spec.ForProvider.Name
	}
	return cr.GetName()
}
