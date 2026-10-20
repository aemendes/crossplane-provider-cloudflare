// Package dnsrecord manages the lifecycle of DNSRecord managed resources.
package dnsrecord

import (
	"context"
	"fmt"

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

	v1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/dnsrecord/v1alpha1"
	apisv1alpha1 "github.com/aemendes/crossplane-provider-cloudflare/tunnel/apis/v1alpha1"
	"github.com/aemendes/crossplane-provider-cloudflare/tunnel/internal/clients/cloudflare"
)

const (
	errNotDNSRecord = "managed resource is not a DNSRecord custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errObserve = "cannot observe Cloudflare DNS record"
	errCreate  = "cannot create Cloudflare DNS record"
	errUpdate  = "cannot update Cloudflare DNS record"
	errDelete  = "cannot delete Cloudflare DNS record"
)

// Setup adds a controller that reconciles DNSRecord managed resources.
func Setup(mgr ctrl.Manager, o crossplanecontroller.Options) error {
	name := managed.ControllerName(v1alpha1.DNSRecordKind)

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.SchemeGroupVersion.WithKind(v1alpha1.DNSRecordKind)),
		managed.WithExternalConnecter(&connector{
			kube:        mgr.GetClient(),
			usage:       resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClientFn: cloudflare.NewClient,
		}),
		// Disabled for the same reason as the Tunnel controller: a hostname
		// can only have one CNAME record, so a lost external-name annotation
		// must be able to adopt the existing record by name instead of
		// erroring forever against an "already exists" response.
		managed.WithInitializers(),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithPollInterval(o.PollInterval),
		managed.WithManagementPolicies(),
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(controller.Options{MaxConcurrentReconciles: o.MaxConcurrentReconciles}).
		For(&v1alpha1.DNSRecord{}).
		Complete(r)
}

// connector produces an ExternalClient by reading the Cloudflare API token out
// of the ProviderConfig referenced by the DNSRecord being reconciled.
type connector struct {
	kube        client.Client
	usage       resource.Tracker
	newClientFn func(apiToken string) *cloudflare.Client
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.DNSRecord)
	if !ok {
		return nil, errors.New(errNotDNSRecord)
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

// external observes, creates, updates and deletes Cloudflare DNS records on behalf of a DNSRecord managed resource.
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
	cr, ok := mg.(*v1alpha1.DNSRecord)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotDNSRecord)
	}

	id := meta.GetExternalName(cr)
	if id == "" {
		// The external-name annotation is unset. Before declaring the
		// resource doesn't exist, check whether a record with this exact
		// hostname already exists: a hostname can only have one CNAME, so if
		// a prior Create succeeded but the pod died before persisting the
		// annotation, a blind retry would fail forever against the
		// already-occupied name.
		found, err := e.findByExactName(ctx, cr.Spec.ForProvider.ZoneID, cr.Spec.ForProvider.Name)
		if err != nil {
			return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
		}
		if found == nil {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		meta.SetExternalName(cr, found.ID)
		obs := e.observed(cr, found)
		// Observe's annotation changes aren't persisted unless we say so —
		// flag this one as a late-init so the reconciler writes the adopted
		// external-name back to the API server instead of re-discovering it
		// via a ListDNSRecordsByExactName call on every subsequent poll.
		obs.ResourceLateInitialized = true
		return obs, nil
	}

	r, err := e.client.GetDNSRecord(ctx, cr.Spec.ForProvider.ZoneID, id)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, errors.Wrap(err, errObserve)
	}

	return e.observed(cr, r), nil
}

// observed records the given external state on cr and returns the resulting
// ExternalObservation. r is assumed to exist (a successful Get, or a
// Create/List match).
func (e *external) observed(cr *v1alpha1.DNSRecord, r *cloudflare.DNSRecord) managed.ExternalObservation {
	cr.SetConditions(xpv1.Available())
	cr.Status.AtProvider = v1alpha1.DNSRecordObservation{
		ID:      r.ID,
		Content: r.Content,
	}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: r.Content == tunnelTarget(cr) && r.Proxied == cr.Spec.ForProvider.Proxied,
	}
}

// findByExactName searches Cloudflare for a DNS record with exactly the given
// name, returning nil if none is found.
func (e *external) findByExactName(ctx context.Context, zoneID, name string) (*cloudflare.DNSRecord, error) {
	candidates, err := e.client.ListDNSRecordsByExactName(ctx, zoneID, name)
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
	cr, ok := mg.(*v1alpha1.DNSRecord)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotDNSRecord)
	}

	cr.SetConditions(xpv1.Creating())

	r, err := e.client.CreateDNSRecord(ctx, cr.Spec.ForProvider.ZoneID, cr.Spec.ForProvider.Name, tunnelTarget(cr), cr.Spec.ForProvider.Proxied)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreate)
	}

	meta.SetExternalName(cr, r.ID)

	return managed.ExternalCreation{}, nil
}

func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.DNSRecord)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotDNSRecord)
	}

	if _, err := e.client.UpdateDNSRecord(ctx, cr.Spec.ForProvider.ZoneID, meta.GetExternalName(cr), cr.Spec.ForProvider.Name, tunnelTarget(cr), cr.Spec.ForProvider.Proxied); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdate)
	}

	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.DNSRecord)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotDNSRecord)
	}

	cr.SetConditions(xpv1.Deleting())

	if err := e.client.DeleteDNSRecord(ctx, cr.Spec.ForProvider.ZoneID, meta.GetExternalName(cr)); err != nil {
		return managed.ExternalDelete{}, errors.Wrap(err, errDelete)
	}

	return managed.ExternalDelete{}, nil
}

// tunnelTarget is the CNAME target every tunnel-routing DNS record points at.
func tunnelTarget(cr *v1alpha1.DNSRecord) string {
	return fmt.Sprintf("%s.cfargotunnel.com", cr.Spec.ForProvider.TunnelID)
}
