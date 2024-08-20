/*
Copyright The KCL authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	"github.com/fluxcd/cli-utils/pkg/object"
	apiacl "github.com/fluxcd/pkg/apis/acl"
	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	"github.com/fluxcd/pkg/apis/meta"
	"github.com/fluxcd/pkg/http/fetch"
	"github.com/fluxcd/pkg/runtime/acl"
	runtimeClient "github.com/fluxcd/pkg/runtime/client"
	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/fluxcd/pkg/runtime/predicates"
	ssautil "github.com/fluxcd/pkg/ssa/utils"
	"github.com/fluxcd/pkg/tar"
	sw "github.com/fluxcd/source-watcher/controllers"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	kuberecorder "k8s.io/client-go/tools/record"

	helper "github.com/fluxcd/pkg/runtime/controller"
	"github.com/fluxcd/pkg/ssa"
	"github.com/fluxcd/pkg/ssa/normalize"
	"github.com/fluxcd/pkg/ssa/utils"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	"github.com/kcl-lang/flux-kcl-controller/api/v1alpha1"
	"github.com/kcl-lang/flux-kcl-controller/internal/inventory"
	"github.com/kcl-lang/flux-kcl-controller/internal/kcl"
	intpredicates "github.com/kcl-lang/flux-kcl-controller/internal/predicates"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// KCLRunReconciler reconciles a KCLRun object
type KCLRunReconciler struct {
	client.Client
	kuberecorder.EventRecorder
	helper.Metrics

	ControllerName   string
	StatusPoller     *polling.StatusPoller
	PollingOpts      polling.Options
	GetClusterConfig func() (*rest.Config, error)
	ClientOpts       runtimeClient.Options
	KubeConfigOpts   runtimeClient.KubeConfigOptions

	DefaultServiceAccount   string
	DisallowedFieldManagers []string
	artifactFetcher         *fetch.ArchiveFetcher

	statusManager string
}

type KCLRunReconcilerOptions struct {
	HTTPRetry int
}

// SetupWithManager sets up the controller with the Manager.
func (r *KCLRunReconciler) SetupWithManager(mgr ctrl.Manager, opts KCLRunReconcilerOptions) error {
	// Setup the artifact fetcher
	r.artifactFetcher = fetch.New(
		fetch.WithRetries(opts.HTTPRetry),
		fetch.WithMaxDownloadSize(tar.UnlimitedUntarSize),
		fetch.WithUntar(tar.WithMaxUntarSize(tar.UnlimitedUntarSize)),
		fetch.WithHostnameOverwrite(os.Getenv("SOURCE_CONTROLLER_LOCALHOST")),
	)
	r.statusManager = "gotk-flux-kcl-controller"
	// New controller
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KCLRun{}, builder.WithPredicates(
			predicate.Or(predicate.GenerationChangedPredicate{}, predicates.ReconcileRequestedPredicate{}),
		)).
		Watches(
			&sourcev1.GitRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForRevisionChangeOf()),
			builder.WithPredicates(sw.GitRepositoryRevisionChangePredicate{}),
		).
		Watches(
			&sourcev1beta2.OCIRepository{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForOCIRepositoryChange),
			builder.WithPredicates(intpredicates.SourceRevisionChangePredicate{}),
		).
		WithOptions(controller.Options{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=krm.kcl.dev.fluxcd,resources=kclruns,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=krm.kcl.dev.fluxcd,resources=kclruns/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=krm.kcl.dev.fluxcd,resources=kclruns/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the KCLRun object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *KCLRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)
	reconcileStart := time.Now()
	// Get KCL source object
	var obj v1alpha1.KCLRun
	if err := r.Get(ctx, req.NamespacedName, &obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Initialize the runtime patcher with the current version of the object.
	patcher := patch.NewSerialPatcher(&obj, r.Client)

	// Finalise the reconciliation and report the results.
	defer func() {
		// Patch finalizers, status and conditions.
		if err := r.finalizeStatus(ctx, &obj, patcher); err != nil {
			retErr = kerrors.NewAggregate([]error{retErr, err})
		}

		// Record Prometheus metrics.
		r.Metrics.RecordReadiness(ctx, &obj)
		r.Metrics.RecordDuration(ctx, &obj, reconcileStart)
		r.Metrics.RecordSuspend(ctx, &obj, obj.Spec.Suspend)

		// Log and emit success event.
		if conditions.IsReady(&obj) {
			msg := fmt.Sprintf("Reconciliation finished in %s, next run in %s",
				time.Since(reconcileStart).String(),
				obj.Spec.Interval.Duration.String())
			log.Info(msg, "revision", obj.Status.LastAttemptedRevision)
			r.event(&obj, obj.Status.LastAppliedRevision, eventv1.EventSeverityInfo, msg,
				map[string]string{
					v1alpha1.GroupVersion.Group + "/" + eventv1.MetaCommitStatusKey: eventv1.MetaCommitStatusUpdateValue,
				})
		}
	}()

	// Prune managed resources if the object is under deletion.
	if !obj.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &obj)
	}

	// Add finalizer first if it doesn't exist to avoid the race condition
	// between init and delete.
	// Note: Finalizers in general can only be added when the deletionTimestamp
	// is not set.
	if !controllerutil.ContainsFinalizer(&obj, v1alpha1.KCLRunFinalizer) {
		controllerutil.AddFinalizer(&obj, v1alpha1.KCLRunFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	// Skip reconciliation if the object is suspended.
	if obj.Spec.Suspend {
		log.Info("Reconciliation is suspended for this object")
		return ctrl.Result{}, nil
	}

	source, err := r.getSource(ctx, &obj)
	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", err)
		if apierrors.IsNotFound(err) {
			msg := fmt.Sprintf("Source '%s' not found", obj.Spec.SourceRef.String())
			log.Info(msg)
			return ctrl.Result{RequeueAfter: obj.GetRetryInterval()}, nil
		}

		if acl.IsAccessDenied(err) {
			conditions.MarkFalse(&obj, meta.ReadyCondition, apiacl.AccessDeniedReason, "%s", err)
			log.Error(err, "Access denied to cross-namespace source")
			r.event(&obj, "unknown", eventv1.EventSeverityError, err.Error(), nil)
			return ctrl.Result{RequeueAfter: obj.GetRetryInterval()}, nil
		}
		// Retry with backoff on transient errors.
		return ctrl.Result{}, err
	}
	artifact := source.GetArtifact()
	progressingMsg := fmt.Sprintf("new revision detected %s", artifact.Revision)
	log.Info(progressingMsg)
	conditions.MarkUnknown(&obj, meta.ReadyCondition, meta.ProgressingReason, "Reconciliation in progress")
	conditions.MarkReconciling(&obj, meta.ProgressingReason, progressingMsg)

	if err := r.patch(ctx, &obj, patcher); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	// Create a snapshot of the current inventory.
	oldInventory := inventory.New()
	if obj.Status.Inventory != nil {
		obj.Status.Inventory.DeepCopyInto(oldInventory)
	}

	// Create tmp dir
	tmpDir, err := os.MkdirTemp("", obj.Name)
	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, sourcev1.DirCreationFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to create temp dir, error: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	log.Info("fetching......")
	// Download and extract artifact
	if err := r.artifactFetcher.Fetch(artifact.URL, artifact.Digest, tmpDir); err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, "failed fetch artifacts", err.Error())
		log.Error(err, "unable to fetch artifact")
		return ctrl.Result{}, err
	}
	// Check build path exists
	dirPath, err := securejoin.SecureJoin(tmpDir, obj.Spec.Path)
	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", err)
		return ctrl.Result{}, err
	}
	if _, err := os.Stat(dirPath); err != nil {
		err = fmt.Errorf("KCL package path not found: %w", err)
		conditions.MarkFalse(&obj, meta.ReadyCondition, meta.ArtifactFailedReason, "%s", err)
		return ctrl.Result{}, err
	}
	// Compile the KCL source code into the Kubernetes manifests
	res, err := kcl.CompileKclPackage(&obj, dirPath)
	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, "FetchFailed", err.Error())
		log.Error(err, "failed to compile the KCL source code")
		return ctrl.Result{}, err
	}
	objects, err := utils.ReadObjects(bytes.NewReader(([]byte(res.GetRawYamlResult()))))
	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, "CompileFailed", err.Error())
		log.Error(err, "failed to compile the yaml str into kubernetes manifests")
		return ctrl.Result{}, err
	}
	log.Info(fmt.Sprintf("compile result %s", res.GetRawYamlResult()))

	// Configure the Kubernetes client for impersonation.
	impersonation := runtimeClient.NewImpersonator(
		r.Client,
		r.StatusPoller,
		r.PollingOpts,
		obj.Spec.KubeConfig,
		r.KubeConfigOpts,
		r.DefaultServiceAccount,
		obj.Spec.ServiceAccountName,
		obj.GetNamespace(),
	)
	// Create the Kubernetes client that runs under impersonation.
	kubeClient, statusPoller, err := impersonation.GetClient(ctx)
	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return ctrl.Result{}, fmt.Errorf("failed to build kube client: %w", err)
	}

	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, "RESTClientError", "%s", err)
		return ctrl.Result{}, err
	}
	// Remove any stale corresponding Ready=False condition with Unknown.
	if conditions.HasAnyReason(&obj, meta.ReadyCondition, "RESTClientError") {
		conditions.MarkUnknown(&obj, meta.ReadyCondition, meta.ProgressingReason, "reconciliation in progress")
	}

	rm := ssa.NewResourceManager(kubeClient, statusPoller, ssa.Owner{
		Field: "kcl-controller",
		Group: obj.GroupVersionKind().Group,
	})
	rm.SetOwnerLabels(objects, obj.GetName(), obj.GetNamespace())

	// Apply the manifests
	log.Info(fmt.Sprintf("applying %s", obj.GetName()))
	// Validate and apply resources in stages.
	drifted, changeSet, err := r.apply(ctx, rm, &obj, artifact.Revision, objects)
	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, "ApplyFailed", err.Error())
		err = fmt.Errorf("failed to run server-side apply: %w", err)
		return ctrl.Result{}, err
	}

	log.Info("successfully applied kcl resources")

	// Create an inventory from the reconciled resources.
	newInventory := inventory.New()
	err = inventory.AddChangeSet(newInventory, changeSet)
	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return ctrl.Result{}, err
	}

	// Set last applied inventory in status.
	obj.Status.Inventory = newInventory

	// Detect stale resources which are subject to garbage collection.
	staleObjects, err := inventory.Diff(oldInventory, newInventory)
	if err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, meta.ReconciliationFailedReason, "%s", err)
		return ctrl.Result{}, err
	}

	// Run garbage collection for stale resources that do not have pruning disabled.
	if _, err := r.prune(ctx, rm, &obj, artifact.Revision, staleObjects); err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, meta.PruneFailedReason, "%s", err)
		return ctrl.Result{}, err
	}

	// Run the health checks for the last applied resources.
	isNewRevision := !source.GetArtifact().HasRevision(obj.Status.LastAppliedRevision)
	if err := r.checkHealth(ctx,
		rm,
		patcher,
		&obj,
		artifact.Revision,
		isNewRevision,
		drifted,
		changeSet.ToObjMetadataSet()); err != nil {
		conditions.MarkFalse(&obj, meta.ReadyCondition, meta.HealthCheckFailedReason, "%s", err)
		return ctrl.Result{}, err
	}

	// Set last applied revision.
	obj.Status.LastAppliedRevision = artifact.Revision

	// Mark the object as ready.
	conditions.MarkTrue(&obj,
		meta.ReadyCondition,
		meta.ReconciliationSucceededReason,
		fmt.Sprintf("Applied revision: %s", artifact.Revision))

	if err := r.Status().Update(ctx, &obj); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *KCLRunReconciler) getSource(ctx context.Context,
	obj *v1alpha1.KCLRun) (sourcev1.Source, error) {
	var src sourcev1.Source
	sourceNamespace := obj.GetNamespace()
	if obj.Spec.SourceRef.Namespace != "" {
		sourceNamespace = obj.Spec.SourceRef.Namespace
	}
	namespacedName := types.NamespacedName{
		Namespace: sourceNamespace,
		Name:      obj.Spec.SourceRef.Name,
	}

	switch obj.Spec.SourceRef.Kind {
	case sourcev1.GitRepositoryKind:
		var repository sourcev1.GitRepository
		err := r.Client.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return src, err
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &repository
	case sourcev1beta2.OCIRepositoryKind:
		var repository sourcev1beta2.OCIRepository
		err := r.Client.Get(ctx, namespacedName, &repository)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return src, err
			}
			return src, fmt.Errorf("unable to get source '%s': %w", namespacedName, err)
		}
		src = &repository
	default:
		return src, fmt.Errorf("source `%s` kind '%s' not supported",
			obj.Spec.SourceRef.Name, obj.Spec.SourceRef.Kind)
	}
	return src, nil
}

func (r *KCLRunReconciler) requestsForRevisionChangeOf() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		log := ctrl.LoggerFrom(ctx)
		repo, ok := obj.(*sourcev1.GitRepository)

		if !ok {
			log.Error(fmt.Errorf("expected an object conformed with GetArtifact() method, but got a %T", obj),
				"failed to get reconcile requests for revision change")
			return nil
		}
		// If we do not have an artifact, we have no requests to make
		if repo.GetArtifact() == nil {
			return nil
		}

		var list v1alpha1.KCLRunList
		if err := r.List(ctx, &list); err != nil {
			log.Error(err, "failed to list objects for revision change")
			return nil
		}
		log.Info(fmt.Sprintf("found %d objects for revision change", len(list.Items)))

		var reqs []reconcile.Request
		for i, d := range list.Items {
			log.Info(fmt.Sprintf("d: %v\n", d))
			// If the KCL source is ready and the revision of the artifact equals
			// to the last attempted revision, we should not make a request for this Kustomization
			if conditions.IsReady(&list.Items[i]) &&
				repo.Name == d.Spec.SourceRef.Name &&
				repo.Namespace == d.Spec.SourceRef.Namespace &&
				repo.GetArtifact().HasRevision(d.Status.LastAttemptedRevision) {
				continue
			}
			log.Info(fmt.Sprintf("revision of %s/%s changed", repo.GetArtifact().Revision, d.Status.LastAttemptedRevision))
			log.Info(fmt.Sprintf("enqueueing %s/%s for revision change", d.GetNamespace(), d.GetName()))
			reqs = append(reqs, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: d.GetNamespace(),
					Name:      d.GetName(),
				},
			})
		}

		return reqs
	}
}

func (r *KCLRunReconciler) requestsForOCIRepositoryChange(ctx context.Context, o client.Object) []reconcile.Request {
	or, ok := o.(*sourcev1beta2.OCIRepository)
	if !ok {
		err := fmt.Errorf("expected an OCIRepository, got %T", o)
		ctrl.LoggerFrom(ctx).Error(err, "failed to get requests for OCIRepository change")
		return nil
	}
	// If we do not have an artifact, we have no requests to make
	if or.GetArtifact() == nil {
		return nil
	}

	var list v1alpha1.KCLRunList
	if err := r.List(ctx, &list, client.MatchingFields{
		".metadata.source": client.ObjectKeyFromObject(or).String(),
	}); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list HelmReleases for OCIRepository change")
		return nil
	}

	var reqs []reconcile.Request
	for i, hr := range list.Items {
		// If the HelmRelease is ready and the digest of the artifact equals to the
		// last attempted revision digest, we should not make a request for this HelmRelease,
		// likewise if we cannot retrieve the artifact digest.
		digest := extractDigest(or.GetArtifact().Revision)
		if digest == "" {
			ctrl.LoggerFrom(ctx).Error(fmt.Errorf("wrong digest for %T", or), "failed to get requests for OCIRepository change")
			continue
		}

		if digest == hr.Status.LastAttemptedRevisionDigest {
			continue
		}

		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return reqs
}

func (r *KCLRunReconciler) checkHealth(ctx context.Context,
	manager *ssa.ResourceManager,
	patcher *patch.SerialPatcher,
	obj *v1alpha1.KCLRun,
	revision string,
	isNewRevision bool,
	drifted bool,
	objects object.ObjMetadataSet) error {
	if len(obj.Spec.HealthChecks) == 0 && !obj.Spec.Wait {
		conditions.Delete(obj, meta.HealthyCondition)
		return nil
	}

	checkStart := time.Now()
	var err error
	if !obj.Spec.Wait {
		objects, err = inventory.ReferenceToObjMetadataSet(obj.Spec.HealthChecks)
		if err != nil {
			return err
		}
	}

	if len(objects) == 0 {
		conditions.Delete(obj, meta.HealthyCondition)
		return nil
	}

	// Guard against deadlock (waiting on itself).
	var toCheck []object.ObjMetadata
	for _, o := range objects {
		if o.GroupKind.Kind == v1alpha1.KCLRunKind &&
			o.Name == obj.GetName() &&
			o.Namespace == obj.GetNamespace() {
			continue
		}
		toCheck = append(toCheck, o)
	}

	// Find the previous health check result.
	wasHealthy := apimeta.IsStatusConditionTrue(obj.Status.Conditions, meta.HealthyCondition)

	// Update status with the reconciliation progress.
	message := fmt.Sprintf("Running health checks for revision %s with a timeout of %s", revision, obj.GetTimeout().String())
	conditions.MarkReconciling(obj, meta.ProgressingReason, "%s", message)
	conditions.MarkUnknown(obj, meta.HealthyCondition, meta.ProgressingReason, "%s", message)
	if err := r.patch(ctx, obj, patcher); err != nil {
		return fmt.Errorf("unable to update the healthy status to progressing: %w", err)
	}

	// Check the health with a default timeout of 30sec shorter than the reconciliation interval.
	if err := manager.WaitForSet(toCheck, ssa.WaitOptions{
		Interval: 5 * time.Second,
		Timeout:  obj.GetTimeout(),
	}); err != nil {
		conditions.MarkFalse(obj, meta.ReadyCondition, meta.HealthCheckFailedReason, "%s", err)
		conditions.MarkFalse(obj, meta.HealthyCondition, meta.HealthCheckFailedReason, "%s", err)
		return fmt.Errorf("health check failed after %s: %w", time.Since(checkStart).String(), err)
	}

	// Emit recovery event if the previous health check failed.
	msg := fmt.Sprintf("Health check passed in %s", time.Since(checkStart).String())
	if !wasHealthy || (isNewRevision && drifted) {
		r.event(obj, revision, eventv1.EventSeverityInfo, msg, nil)
	}

	conditions.MarkTrue(obj, meta.HealthyCondition, meta.SucceededReason, "%s", msg)
	if err := r.patch(ctx, obj, patcher); err != nil {
		return fmt.Errorf("unable to update the healthy status to progressing: %w", err)
	}

	return nil
}

func (r *KCLRunReconciler) prune(ctx context.Context,
	manager *ssa.ResourceManager,
	obj *v1alpha1.KCLRun,
	revision string,
	objects []*unstructured.Unstructured) (bool, error) {
	if !obj.Spec.Prune {
		return false, nil
	}

	log := ctrl.LoggerFrom(ctx)

	opts := ssa.DeleteOptions{
		PropagationPolicy: metav1.DeletePropagationBackground,
		Inclusions:        manager.GetOwnerLabels(obj.Name, obj.Namespace),
		Exclusions: map[string]string{
			fmt.Sprintf("%s/prune", v1alpha1.GroupVersion.Group):     v1alpha1.DisabledValue,
			fmt.Sprintf("%s/reconcile", v1alpha1.GroupVersion.Group): v1alpha1.DisabledValue,
		},
	}

	changeSet, err := manager.DeleteAll(ctx, objects, opts)
	if err != nil {
		return false, err
	}

	// emit event only if the prune operation resulted in changes
	if changeSet != nil && len(changeSet.Entries) > 0 {
		log.Info(fmt.Sprintf("garbage collection completed: %s", changeSet.String()))
		r.event(obj, revision, eventv1.EventSeverityInfo, changeSet.String(), nil)
		return true, nil
	}

	return false, nil
}

func (r *KCLRunReconciler) finalize(ctx context.Context,
	obj *v1alpha1.KCLRun) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	if obj.Spec.Prune &&
		!obj.Spec.Suspend &&
		obj.Status.Inventory != nil &&
		obj.Status.Inventory.Entries != nil {
		objects, _ := inventory.List(obj.Status.Inventory)

		impersonation := runtimeClient.NewImpersonator(
			r.Client,
			r.StatusPoller,
			r.PollingOpts,
			obj.Spec.KubeConfig,
			r.KubeConfigOpts,
			r.DefaultServiceAccount,
			obj.Spec.ServiceAccountName,
			obj.GetNamespace(),
		)
		if impersonation.CanImpersonate(ctx) {
			kubeClient, _, err := impersonation.GetClient(ctx)
			if err != nil {
				return ctrl.Result{}, err
			}

			resourceManager := ssa.NewResourceManager(kubeClient, nil, ssa.Owner{
				Field: r.ControllerName,
				Group: v1alpha1.GroupVersion.Group,
			})

			opts := ssa.DeleteOptions{
				PropagationPolicy: metav1.DeletePropagationBackground,
				Inclusions:        resourceManager.GetOwnerLabels(obj.Name, obj.Namespace),
				Exclusions: map[string]string{
					fmt.Sprintf("%s/prune", v1alpha1.GroupVersion.Group):     v1alpha1.DisabledValue,
					fmt.Sprintf("%s/reconcile", v1alpha1.GroupVersion.Group): v1alpha1.DisabledValue,
				},
			}

			changeSet, err := resourceManager.DeleteAll(ctx, objects, opts)
			if err != nil {
				r.event(obj, obj.Status.LastAppliedRevision, eventv1.EventSeverityError, "pruning for deleted resource failed", nil)
				// Return the error so we retry the failed garbage collection
				return ctrl.Result{}, err
			}

			if changeSet != nil && len(changeSet.Entries) > 0 {
				r.event(obj, obj.Status.LastAppliedRevision, eventv1.EventSeverityInfo, changeSet.String(), nil)
			}
		} else {
			// when the account to impersonate is gone, log the stale objects and continue with the finalization
			msg := fmt.Sprintf("unable to prune objects: \n%s", ssautil.FmtUnstructuredList(objects))
			log.Error(fmt.Errorf("skiping pruning, failed to find account to impersonate"), msg)
			r.event(obj, obj.Status.LastAppliedRevision, eventv1.EventSeverityError, msg, nil)
		}
	}

	// Remove our finalizer from the list and update it
	controllerutil.RemoveFinalizer(obj, v1alpha1.KCLRunFinalizer)
	// Stop reconciliation as the object is being deleted
	return ctrl.Result{}, nil
}

func (r *KCLRunReconciler) event(obj *v1alpha1.KCLRun,
	revision, severity, msg string,
	metadata map[string]string) {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if revision != "" {
		metadata[v1alpha1.GroupVersion.Group+"/revision"] = revision
	}

	reason := severity
	if r := conditions.GetReason(obj, meta.ReadyCondition); r != "" {
		reason = r
	}

	eventtype := "Normal"
	if severity == eventv1.EventSeverityError {
		eventtype = "Warning"
	}

	r.EventRecorder.AnnotatedEventf(obj, metadata, eventtype, reason, msg)
}

func (r *KCLRunReconciler) apply(ctx context.Context,
	manager *ssa.ResourceManager,
	obj *v1alpha1.KCLRun,
	revision string,
	objects []*unstructured.Unstructured) (bool, *ssa.ChangeSet, error) {
	log := ctrl.LoggerFrom(ctx)

	if err := normalize.UnstructuredList(objects); err != nil {
		return false, nil, err
	}

	if cmeta := obj.Spec.CommonMetadata; cmeta != nil {
		ssautil.SetCommonMetadata(objects, cmeta.Labels, cmeta.Annotations)
	}

	applyOpts := ssa.DefaultApplyOptions()
	applyOpts.Force = obj.Spec.Force
	applyOpts.ExclusionSelector = map[string]string{
		fmt.Sprintf("%s/reconcile", v1alpha1.GroupVersion.Group): v1alpha1.DisabledValue,
		fmt.Sprintf("%s/ssa", v1alpha1.GroupVersion.Group):       v1alpha1.IgnoreValue,
	}
	applyOpts.IfNotPresentSelector = map[string]string{
		fmt.Sprintf("%s/ssa", v1alpha1.GroupVersion.Group): v1alpha1.IfNotPresentValue,
	}
	applyOpts.ForceSelector = map[string]string{
		fmt.Sprintf("%s/force", v1alpha1.GroupVersion.Group): v1alpha1.EnabledValue,
	}

	fieldManagers := []ssa.FieldManager{
		{
			// to undo changes made with 'kubectl apply --server-side --force-conflicts'
			Name:          "kubectl",
			OperationType: metav1.ManagedFieldsOperationApply,
		},
		{
			// to undo changes made with 'kubectl apply'
			Name:          "kubectl",
			OperationType: metav1.ManagedFieldsOperationUpdate,
		},
		{
			// to undo changes made with 'kubectl apply'
			Name:          "before-first-apply",
			OperationType: metav1.ManagedFieldsOperationUpdate,
		},
		{
			// to undo changes made by the controller before SSA
			Name:          r.ControllerName,
			OperationType: metav1.ManagedFieldsOperationUpdate,
		},
	}

	for _, fieldManager := range r.DisallowedFieldManagers {
		fieldManagers = append(fieldManagers, ssa.FieldManager{
			Name:          fieldManager,
			OperationType: metav1.ManagedFieldsOperationApply,
		})
		// to undo changes made by the controller before SSA
		fieldManagers = append(fieldManagers, ssa.FieldManager{
			Name:          fieldManager,
			OperationType: metav1.ManagedFieldsOperationUpdate,
		})
	}

	applyOpts.Cleanup = ssa.ApplyCleanupOptions{
		Annotations: []string{
			// remove the kubectl annotation
			corev1.LastAppliedConfigAnnotation,
			// remove deprecated fluxcd.io annotations
			"kustomize.toolkit.fluxcd.io/checksum",
			"fluxcd.io/sync-checksum",
		},
		Labels: []string{
			// remove deprecated fluxcd.io labels
			"fluxcd.io/sync-gc-mark",
		},
		FieldManagers: fieldManagers,
		Exclusions: map[string]string{
			fmt.Sprintf("%s/ssa", v1alpha1.GroupVersion.Group): v1alpha1.MergeValue,
		},
	}

	// contains only CRDs and Namespaces
	var defStage []*unstructured.Unstructured

	// contains only Kubernetes Class types e.g.: RuntimeClass, PriorityClass,
	// StorageClass, VolumeSnapshotClass, IngressClass, GatewayClass, ClusterClass, etc
	var classStage []*unstructured.Unstructured

	// contains all objects except for CRDs, Namespaces and Class type objects
	var resStage []*unstructured.Unstructured

	// contains the objects' metadata after apply
	resultSet := ssa.NewChangeSet()

	for _, u := range objects {
		switch {
		case ssautil.IsClusterDefinition(u):
			defStage = append(defStage, u)
		case strings.HasSuffix(u.GetKind(), "Class"):
			classStage = append(classStage, u)
		default:
			resStage = append(resStage, u)
		}

	}

	var changeSetLog strings.Builder

	// validate, apply and wait for CRDs and Namespaces to register
	if len(defStage) > 0 {
		changeSet, err := manager.ApplyAll(ctx, defStage, applyOpts)
		if err != nil {
			return false, nil, err
		}

		if changeSet != nil && len(changeSet.Entries) > 0 {
			resultSet.Append(changeSet.Entries)

			log.Info("server-side apply for cluster definitions completed", "output", changeSet.ToMap())
			for _, change := range changeSet.Entries {
				if HasChanged(change.Action) {
					changeSetLog.WriteString(change.String() + "\n")
				}
			}

			if err := manager.WaitForSet(changeSet.ToObjMetadataSet(), ssa.WaitOptions{
				Interval: 2 * time.Second,
				Timeout:  obj.GetTimeout(),
			}); err != nil {
				return false, nil, err
			}
		}
	}

	// validate, apply and wait for Class type objects to register
	if len(classStage) > 0 {
		changeSet, err := manager.ApplyAll(ctx, classStage, applyOpts)
		if err != nil {
			return false, nil, err
		}

		if changeSet != nil && len(changeSet.Entries) > 0 {
			resultSet.Append(changeSet.Entries)

			log.Info("server-side apply for cluster class types completed", "output", changeSet.ToMap())
			for _, change := range changeSet.Entries {
				if HasChanged(change.Action) {
					changeSetLog.WriteString(change.String() + "\n")
				}
			}

			if err := manager.WaitForSet(changeSet.ToObjMetadataSet(), ssa.WaitOptions{
				Interval: 2 * time.Second,
				Timeout:  obj.GetTimeout(),
			}); err != nil {
				return false, nil, err
			}
		}
	}

	// sort by kind, validate and apply all the others objects
	sort.Sort(ssa.SortableUnstructureds(resStage))
	if len(resStage) > 0 {
		changeSet, err := manager.ApplyAll(ctx, resStage, applyOpts)
		if err != nil {
			return false, nil, fmt.Errorf("%w\n%s", err, changeSetLog.String())
		}

		if changeSet != nil && len(changeSet.Entries) > 0 {
			resultSet.Append(changeSet.Entries)

			log.Info("server-side apply completed", "output", changeSet.ToMap(), "revision", revision)
			for _, change := range changeSet.Entries {
				if HasChanged(change.Action) {
					changeSetLog.WriteString(change.String() + "\n")
				}
			}
		}
	}

	// emit event only if the server-side apply resulted in changes
	applyLog := strings.TrimSuffix(changeSetLog.String(), "\n")
	if applyLog != "" {
		r.event(obj, revision, eventv1.EventSeverityInfo, applyLog, nil)
	}

	return applyLog != "", resultSet, nil
}

func (r *KCLRunReconciler) finalizeStatus(ctx context.Context,
	obj *v1alpha1.KCLRun,
	patcher *patch.SerialPatcher) error {
	// Set the value of the reconciliation request in status.
	if v, ok := meta.ReconcileAnnotationValue(obj.GetAnnotations()); ok {
		obj.Status.LastHandledReconcileAt = v
	}

	// Remove the Reconciling condition and update the observed generation
	// if the reconciliation was successful.
	if conditions.IsTrue(obj, meta.ReadyCondition) {
		conditions.Delete(obj, meta.ReconcilingCondition)
		obj.Status.ObservedGeneration = obj.Generation
	}

	// Set the Reconciling reason to ProgressingWithRetry if the
	// reconciliation has failed.
	if conditions.IsFalse(obj, meta.ReadyCondition) &&
		conditions.Has(obj, meta.ReconcilingCondition) {
		rc := conditions.Get(obj, meta.ReconcilingCondition)
		rc.Reason = meta.ProgressingWithRetryReason
		conditions.Set(obj, rc)
	}

	// Patch finalizers, status and conditions.
	return r.patch(ctx, obj, patcher)
}

func (r *KCLRunReconciler) patch(ctx context.Context,
	obj *v1alpha1.KCLRun,
	patcher *patch.SerialPatcher) (retErr error) {

	// Configure the runtime patcher.
	patchOpts := []patch.Option{}
	ownedConditions := []string{
		meta.HealthyCondition,
		meta.ReadyCondition,
		meta.ReconcilingCondition,
		meta.StalledCondition,
	}
	patchOpts = append(patchOpts,
		patch.WithOwnedConditions{Conditions: ownedConditions},
		patch.WithForceOverwriteConditions{},
		patch.WithFieldOwner(r.statusManager),
	)

	// Patch the object status, conditions and finalizers.
	if err := patcher.Patch(ctx, obj, patchOpts...); err != nil {
		if !obj.GetDeletionTimestamp().IsZero() {
			err = kerrors.FilterOut(err, func(e error) bool { return apierrors.IsNotFound(e) })
		}
		retErr = kerrors.NewAggregate([]error{retErr, err})
		if retErr != nil {
			return retErr
		}
	}

	return nil
}
