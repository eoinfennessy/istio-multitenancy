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
	"k8s.io/apimachinery/pkg/labels"
	"slices"
	"strings"

	"istio.io/api/annotation"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
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
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	log := logf.FromContext(ctx)
	log.Info("Reconcile started")

	// Fetch Zone resource
	z := &v1alpha1.Zone{}
	if err := r.Client.Get(ctx, req.NamespacedName, z); err != nil {
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

	// Get lists of Services that should be part of the Zone
	servicesLists := make([]*corev1.ServiceList, len(z.Spec.Namespaces))
	ch := make(chan error)
	for i, ns := range z.Spec.Namespaces {
		servicesLists[i] = &corev1.ServiceList{}
		go func(ch chan error) {
			ch <- r.List(ctx, servicesLists[i], &client.ListOptions{Namespace: ns})
		}(ch)
	}
	for range z.Spec.Namespaces {
		err = <-ch
		if err != nil {
			log.Error(err, "Failed to list Services")
			return ctrl.Result{}, err
		}
	}

	// Set labels and annotations on Services
	var serviceCount int
	exportToAnnotationValue := strings.Join(z.Spec.Namespaces, ",")
	for _, servicesList := range servicesLists {
		serviceCount += len(servicesList.Items)
		for _, service := range servicesList.Items {
			go func(ch chan error) {
				var metaChanged bool
				if labelVal, exists := service.GetLabels()[constants.ZoneLabel]; exists {
					// Return ZoneConflictError if Service is currently part of another Zone
					if labelVal != req.Name {
						err := &pkgerrors.ZoneConflictError{
							Err: errors.New(fmt.Sprintf("Service %s in namespace %s is currently part of zone %s", service.Name, service.Namespace, labelVal)),
						}
						ch <- err
						return
					}
				} else {
					service.SetLabels(map[string]string{constants.ZoneLabel: req.Name})
					metaChanged = true
				}

				if service.GetAnnotations()[annotation.NetworkingExportTo.Name] != exportToAnnotationValue {
					service.SetAnnotations(map[string]string{annotation.NetworkingExportTo.Name: exportToAnnotationValue})
					metaChanged = true
				}
				if metaChanged {
					log.Info("Updating service", "namespace", service.Namespace, "name", service.Name)
					ch <- r.Update(ctx, &service)
				}
				ch <- nil
			}(ch)
		}
	}
	for range serviceCount {
		err = <-ch
		if err != nil {
			if zoneConflictErr, ok := err.(*pkgerrors.ZoneConflictError); ok {
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, zoneConflictErr.Error())
				// The Zone is currently unreconcilable; do not requeue
				return ctrl.Result{}, nil
			} else {
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, "Failed to update Service")
				return ctrl.Result{}, err
			}
		}
	}

	// Clean up Services that should no longer be part of the Zone
	// TODO: limit the list options to only include Services in namespaces outside the Zone (I tried below, but it always returns an empty list)
	//fieldSelectors := make([]fields.Selector, len(z.Spec.Namespaces))
	//for i, ns := range z.Spec.Namespaces {
	//	fieldSelectors[i] = fields.OneTermNotEqualSelector(".metadata.namespace", ns)
	//	log.V(1).Info("fieldSelector", "requirements", fieldSelectors[i].Requirements())
	//}

	services := corev1.ServiceList{}
	if err = r.List(ctx, &services, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{constants.ZoneLabel: req.Name}),
		//FieldSelector: fields.AndSelectors(fieldSelectors...),
	}); err != nil {
		z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, "Failed to list Services")
		return ctrl.Result{}, err
	}

	var updatedServiceCount int
	for _, service := range services.Items {
		if !slices.Contains(z.Spec.Namespaces, service.GetNamespace()) {
			updatedServiceCount++
			go func(ch chan error) {
				delete(service.Labels, constants.ZoneLabel)
				delete(service.Annotations, annotation.NetworkingExportTo.Name)
				ch <- r.Update(ctx, &service)
			}(ch)
		}
	}
	for range updatedServiceCount {
		err = <-ch
		if err != nil {
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, "Failed to update Service")
			return ctrl.Result{}, err
		}
	}

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

	// TODO: Add finalize logic

	controllerutil.RemoveFinalizer(z, constants.ZoneFinalizer)
	if err := r.Update(ctx, z); err != nil {
		log.Error(err, "Failed to update Zone after removing finalizer")
		return err
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
		Watches(
			&corev1.Service{},
			handler.EnqueueRequestsFromMapFunc(r.mapServiceToReconcileRequests),
			// TODO: Narrow down predicates: I think we only care if the labels or annotations have changed
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Complete(r)
}

func extractNamespacesIndex(rawZone client.Object) []string {
	return rawZone.(*v1alpha1.Zone).Spec.Namespaces
}
