/*
Copyright Red Hat

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
	"fmt"
	"slices"
	"strings"

	"istio.io/api/annotation"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openshift-service-mesh/istio-zones/api/v1alpha1"
	"github.com/openshift-service-mesh/istio-zones/pkg/constants"
	pkgerrors "github.com/openshift-service-mesh/istio-zones/pkg/errors"
)

func (r *ZoneReconciler) reconcileServices(ctx context.Context, z *v1alpha1.Zone) error {
	// Get list of Services that should be part of the Zone
	services, err := r.listServicesForZone(ctx, z)
	if err != nil {
		z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, "Failed to list Services")
		return err
	}

	// Update each Service (if required) to include it in the Zone
	ch := make(chan error)
	for _, svc := range services {
		go func() {
			ch <- r.includeServiceInZone(ctx, z, svc)
		}()
	}
	for range services {
		err = <-ch
		if err != nil {
			if pkgerrors.IsUnreconcilableError(err) {
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, err.Error())
			} else {
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, "Failed to include Service in Zone")
			}
			return err
		}
	}

	// Clean up Services that should no longer be part of the Zone
	if err = r.cleanUpServices(ctx, z, func(service corev1.Service) bool {
		return !slices.Contains(z.Spec.Namespaces, service.GetNamespace())
	}); err != nil {
		return err
	}

	return nil
}

// cleanUpServices removes labels and annotations from Services that should no longer be part of the Zone
func (r *ZoneReconciler) cleanUpServices(ctx context.Context, z *v1alpha1.Zone, predicate func(service corev1.Service) bool) error {
	log := logf.FromContext(ctx)

	services := corev1.ServiceList{}
	if err := r.List(ctx, &services, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{constants.ZoneLabel: z.Name}),
	}); err != nil {
		z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, "Failed to list Services")
		return err
	}

	ch := make(chan error)
	var updatedServiceCount int
	for _, svc := range services.Items {
		if predicate(svc) {
			updatedServiceCount++
			go func(ch chan error) {
				delete(svc.Labels, constants.ZoneLabel)
				delete(svc.Annotations, annotation.NetworkingExportTo.Name)
				log.Info(fmt.Sprintf("Excluding Service %s in namespace %s from Zone", svc.Name, svc.Namespace))
				ch <- r.Update(ctx, &svc)
			}(ch)
		}
	}
	for range updatedServiceCount {
		err := <-ch
		if err != nil {
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, "Failed to update Service")
			return err
		}
	}
	return nil
}

// listServicesForZone returns a slice of Services that should be part of the Zone
func (r *ZoneReconciler) listServicesForZone(ctx context.Context, z *v1alpha1.Zone) ([]corev1.Service, error) {
	serviceLists := make([]*corev1.ServiceList, len(z.Spec.Namespaces))
	ch := make(chan error)
	for i, ns := range z.Spec.Namespaces {
		serviceLists[i] = &corev1.ServiceList{}
		go func(ch chan error) {
			ch <- r.List(ctx, serviceLists[i], &client.ListOptions{Namespace: ns})
		}(ch)
	}
	for range z.Spec.Namespaces {
		err := <-ch
		if err != nil {
			return nil, err
		}
	}

	var serviceCount int
	for _, servicesList := range serviceLists {
		serviceCount += len(servicesList.Items)
	}
	services := make([]corev1.Service, 0, serviceCount)
	for _, serviceList := range serviceLists {
		services = append(services, serviceList.Items...)
	}
	return services, nil
}

// includeServiceInZone sets the labels and annotations of the Service to include it in the Zone, and
// updates the Service if either has changed.
func (r *ZoneReconciler) includeServiceInZone(ctx context.Context, z *v1alpha1.Zone, svc corev1.Service) error {
	log := logf.FromContext(ctx)

	var metaChanged bool
	if labelVal, exists := svc.GetLabels()[constants.ZoneLabel]; !exists {
		svc.SetLabels(map[string]string{constants.ZoneLabel: z.Name})
		metaChanged = true
	} else if labelVal != z.Name {
		// Service is currently part of another Zone; Return UnreconcilableError
		return pkgerrors.NewUnreconcilableError(fmt.Sprintf("Service %s in namespace %s is currently part of zone %s", svc.Name, svc.Namespace, labelVal))
	}

	exportToAnnotationValue := strings.Join(getNamespacesForServiceExport(z, svc.GetName(), svc.GetNamespace()), ",")
	if svc.GetAnnotations()[annotation.NetworkingExportTo.Name] != exportToAnnotationValue {
		svc.SetAnnotations(map[string]string{annotation.NetworkingExportTo.Name: exportToAnnotationValue})
		metaChanged = true
	}

	if metaChanged {
		log.Info(fmt.Sprintf("Updating Service %s in namespace %s", svc.Name, svc.Namespace), "exportToAnnotationValue", exportToAnnotationValue)
		return r.Update(ctx, &svc)
	}
	return nil
}

// getNamespacesForServiceExport returns the Zone's namespaces combined with any
// additional exports specified for the Service. If a wildcard has been specified
// in the exports for the Service a slice containing only the wildcard is
// returned.
func getNamespacesForServiceExport(z *v1alpha1.Zone, svcName, svcNamespace string) []string {
	for _, se := range z.Spec.ServiceExports {
		if se.Name == svcName && se.Namespace == svcNamespace {
			if slices.Contains(se.ToNamespaces, constants.Wildcard) {
				return []string{constants.Wildcard}
			} else {
				return append(z.Spec.Namespaces, se.ToNamespaces...)
			}
		}
	}
	return z.Spec.Namespaces
}
