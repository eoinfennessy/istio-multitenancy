/*
Copyright 2024.

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
	"context"
	"errors"
	"fmt"
	"reflect"

	istioclientnetworkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	istioclientsecurityv1 "istio.io/client-go/pkg/apis/security/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/eoinfennessy/istio-multitenancy/api/v1alpha1"
	"github.com/eoinfennessy/istio-multitenancy/pkg/constants"
	pkgerrors "github.com/eoinfennessy/istio-multitenancy/pkg/errors"
)

const (
	namespacesField string = ".spec.namespaces"
)

// ZoneReconciler reconciles a Zone object
type ZoneReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=multitenancy.istio.eoinfennessy.com,resources=zones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=multitenancy.istio.eoinfennessy.com,resources=zones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=multitenancy.istio.eoinfennessy.com,resources=zones/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;update
// +kubebuilder:rbac:groups="networking.istio.io",resources=sidecars,verbs="*"
// +kubebuilder:rbac:groups="security.istio.io",resources=authorizationPolicies,verbs="*"

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	log := logf.FromContext(ctx)
	log.V(1).Info("Reconcile started")

	// Fetch Zone resource
	z := &v1alpha1.Zone{}
	if err = r.Client.Get(ctx, req.NamespacedName, z); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Zone resource not found; Ignoring because it must have been deleted")
			return ctrl.Result{}, nil
		} else {
			log.Error(err, "Failed to fetch Zone resource; Requeuing")
			return ctrl.Result{}, err
		}
	}

	// Update status on return if different to old status
	defer func(oldStatus *v1alpha1.ZoneStatus) {
		if !controllerutil.ContainsFinalizer(z, constants.ZoneFinalizer) {
			// Do not attempt status update when Zone has been deleted
			return
		}
		if !reflect.DeepEqual(oldStatus, z.Status) {
			if statusUpdateErr := r.Status().Update(ctx, z); statusUpdateErr != nil {
				log.Error(err, "Failed to update Zone's status")
				err = errors.Join(err, statusUpdateErr)
			}
		}
	}(z.Status.DeepCopy())

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(z, constants.ZoneFinalizer) {
		if err := r.addFinalizer(ctx, z); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Handle deletion
	if !z.GetDeletionTimestamp().IsZero() && controllerutil.ContainsFinalizer(z, constants.ZoneFinalizer) {
		return ctrl.Result{}, r.finalize(ctx, z)
	}

	if err = r.ensureNamespacesExist(ctx, z); err != nil {
		return ctrl.Result{}, pkgerrors.IgnoreUnreconcilableError(err)
	}

	if err = r.reconcileServices(ctx, z); err != nil {
		return ctrl.Result{}, pkgerrors.IgnoreUnreconcilableError(err)
	}

	if err = r.reconcileSidecars(ctx, z); err != nil {
		return ctrl.Result{}, pkgerrors.IgnoreUnreconcilableError(err)
	}

	if err = r.reconcileAuthorizationPolicies(ctx, z); err != nil {
		return ctrl.Result{}, pkgerrors.IgnoreUnreconcilableError(err)
	}

	z.Status.SetStatusCondition(
		v1alpha1.ConditionTypeReconciled,
		metav1.ConditionTrue,
		v1alpha1.ConditionReasonReconcileSuccess,
		"Finished reconciling Zone",
	)

	return ctrl.Result{}, nil
}

func (r *ZoneReconciler) addFinalizer(ctx context.Context, z *v1alpha1.Zone) error {
	log := logf.FromContext(ctx)
	log.Info("Adding finalizer")
	z.Status.SetStatusCondition(
		v1alpha1.ConditionTypeReconciled,
		metav1.ConditionFalse,
		v1alpha1.ConditionReasonReconcileInProgress,
		"Adding finalizer",
	)
	controllerutil.AddFinalizer(z, constants.ZoneFinalizer)
	if err := r.Update(ctx, z); err != nil {
		log.Error(err, "Failed to update Zone after adding finalizer")
		z.Status.SetStatusCondition(
			v1alpha1.ConditionTypeReconciled,
			metav1.ConditionFalse,
			v1alpha1.ConditionReasonReconcileError,
			"Failed to update Zone",
		)
		return err
	}
	return nil
}

func (r *ZoneReconciler) finalize(ctx context.Context, z *v1alpha1.Zone) error {
	log := logf.FromContext(ctx)
	log.Info("Finalizing Zone resource")

	if err := r.cleanUpServices(ctx, z, func(_ corev1.Service) bool { return true }); err != nil {
		log.Error(err, "Failed to clean up Services")
		return err
	}

	controllerutil.RemoveFinalizer(z, constants.ZoneFinalizer)
	if err := r.Update(ctx, z); err != nil {
		log.Error(err, "Failed to update Zone after removing finalizer")
		return err
	}
	return nil
}

func (r *ZoneReconciler) ensureNamespacesExist(ctx context.Context, z *v1alpha1.Zone) error {
	nsList := &corev1.NamespaceList{}
	if err := r.List(ctx, nsList); err != nil {
		z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, "Failed to list Namespaces")
		return err
	}

	namespaces := make(map[string]struct{}, len(nsList.Items))
	for _, ns := range nsList.Items {
		namespaces[ns.GetName()] = struct{}{}
	}
	for _, ns := range z.Spec.Namespaces {
		if _, exists := namespaces[ns]; !exists {
			msg := fmt.Sprintf("Namespace %s is listed in Zone's spec but does not exist", ns)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, msg)
			return pkgerrors.NewUnreconcilableError(msg)
		}
	}
	return nil
}

func (r *ZoneReconciler) mapServiceToReconcileRequests(ctx context.Context, service client.Object) []reconcile.Request {
	zones := &v1alpha1.ZoneList{}
	if err := r.List(ctx, zones, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(
			namespacesField, service.GetNamespace(),
		),
	}); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, len(zones.Items))
	for i, z := range zones.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: z.GetName(),
			},
		}
	}
	return requests
}

func (r *ZoneReconciler) mapNamespaceToReconcileRequests(ctx context.Context, namespace client.Object) []reconcile.Request {
	zones := &v1alpha1.ZoneList{}
	if err := r.List(ctx, zones, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(
			namespacesField, namespace.GetName(),
		),
	}); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, len(zones.Items))
	for i, z := range zones.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: z.GetName(),
			},
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *ZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create index for Zone on Namespaces field
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.Zone{},
		namespacesField,
		extractNamespacesIndex); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Zone{}).
		Owns(&istioclientnetworkingv1.Sidecar{}).
		Owns(&istioclientsecurityv1.AuthorizationPolicy{}).
		Watches(
			&corev1.Service{},
			handler.EnqueueRequestsFromMapFunc(r.mapServiceToReconcileRequests),
			// TODO: Narrow down predicates: I think we only care if the labels or annotations have changed
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Watches(
			&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(r.mapNamespaceToReconcileRequests),
		).
		Complete(r)
}

func extractNamespacesIndex(rawZone client.Object) []string {
	return rawZone.(*v1alpha1.Zone).Spec.Namespaces
}
