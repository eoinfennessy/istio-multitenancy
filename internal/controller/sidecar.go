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
	"encoding/json"
	"fmt"
	"hash/fnv"
	"reflect"

	istioapinetworkingv1 "istio.io/api/networking/v1"
	istioclientnetworkingv1 "istio.io/client-go/pkg/apis/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openshift-service-mesh/istio-zones/api/v1alpha1"
	"github.com/openshift-service-mesh/istio-zones/pkg/constants"
	pkgerrors "github.com/openshift-service-mesh/istio-zones/pkg/errors"
)

func (r *ZoneReconciler) reconcileSidecars(ctx context.Context, z *v1alpha1.Zone) error {
	zoneHosts := getZoneHosts(z.Spec.Namespaces)

	// Check for Sidecar in each Zone namespace and create/update each if necessary
	for _, ns := range z.Spec.Namespaces {
		sidecarKey := types.NamespacedName{Namespace: ns, Name: constants.SingeltonResourceName}
		if err := r.createOrUpdateSidecar(ctx, z, sidecarKey, func() istioapinetworkingv1.Sidecar {
			return istioapinetworkingv1.Sidecar{
				Egress: []*istioapinetworkingv1.IstioEgressListener{
					{Hosts: zoneHosts},
				},
			}
		}); err != nil {
			return err
		}
	}

	// Check Sidecars for each AdditionalEgress and create/update each if necessary
	for _, ae := range z.Spec.AdditionalEgress {
		// Create hash of WorkloadSelector to use as Sidecar name suffix
		workloadHash, err := hashWorkloadSelector(ae.WorkloadSelector)
		if err != nil {
			msg := fmt.Sprintf("Failed to hash workload selector: %s", err)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
			return err
		}

		sidecarName := getSidecarNameFromHash(workloadHash)
		hosts := append(zoneHosts, ae.Hosts...)

		for _, ns := range z.Spec.Namespaces {
			sidecarKey := types.NamespacedName{Namespace: ns, Name: sidecarName}
			if err := r.createOrUpdateSidecar(ctx, z, sidecarKey, func() istioapinetworkingv1.Sidecar {
				return istioapinetworkingv1.Sidecar{
					Egress: []*istioapinetworkingv1.IstioEgressListener{
						{Hosts: hosts},
					},
					WorkloadSelector: &istioapinetworkingv1.WorkloadSelector{
						Labels: ae.WorkloadSelector,
					},
				}
			}); err != nil {
				return err
			}
		}
	}

	if err := r.cleanUpSidecars(ctx, z); err != nil {
		return err
	}

	return nil
}

func (r *ZoneReconciler) createOrUpdateSidecar(ctx context.Context, z *v1alpha1.Zone, sidecarKey types.NamespacedName, getSidecarSpec func() istioapinetworkingv1.Sidecar) error {
	log := logf.FromContext(ctx)

	sidecar := &istioclientnetworkingv1.Sidecar{}
	err := r.Get(ctx, sidecarKey, sidecar)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			msg := fmt.Sprintf("Error getting Sidecar %s in namespace %s: %s", sidecarKey.Name, sidecarKey.Namespace, err)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
			return err
		}

		// Sidecar doesn't exist; Create it
		log.Info(fmt.Sprintf("Creating Sidecar %s in namespace %s", sidecarKey.Name, sidecarKey.Namespace))
		if err := r.Create(ctx, r.constructSidecar(z, sidecarKey, getSidecarSpec)); err != nil {
			msg := fmt.Sprintf("Error creating Sidecar %s in namespace %s: %s", sidecarKey.Name, sidecarKey.Namespace, err)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
			return err
		}
	} else {
		// Sidecar exists; Check if it belongs to the Zone
		labelVal, exists := sidecar.GetLabels()[constants.ZoneLabel]
		if !exists {
			// TODO: Would checking OwnerReferences be more appropriate?
			msg := fmt.Sprintf("Sidecar %s in namespace %s is not part of the Zone because it does not have a Zone label", sidecar.Name, sidecar.Namespace)
			log.Info(msg)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, msg)
			return pkgerrors.NewUnreconcilableError(msg)
		} else if labelVal != z.GetName() {
			msg := fmt.Sprintf("Sidecar %s in namespace %s is already included in Zone %s", sidecar.Name, sidecar.Namespace, labelVal)
			log.Info(msg)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, msg)
			return pkgerrors.NewUnreconcilableError(msg)
		}

		// Update Sidecar if necessary
		newSpec := getSidecarSpec()
		if !reflect.DeepEqual(sidecar.Spec.Egress, newSpec.Egress) {
			sidecar.Spec.Egress = newSpec.Egress
			log.Info(fmt.Sprintf("Updating Sidecar %s in namespace %s", sidecar.Name, sidecar.Namespace), "egress", newSpec.Egress)
			err = r.Update(ctx, sidecar)
			if err != nil {
				msg := fmt.Sprintf("Error updating Sidecar %s in namespace %s: %s", sidecar.Name, sidecar.Namespace, err)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
				return err
			}
		}
	}
	return nil
}

// cleanUpSidecars deletes Sidecars that should no longer be included in the Zone
func (r *ZoneReconciler) cleanUpSidecars(ctx context.Context, z *v1alpha1.Zone) error {
	log := logf.FromContext(ctx)

	sidecarList := &istioclientnetworkingv1.SidecarList{}
	err := r.List(ctx, sidecarList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{constants.ZoneLabel: z.GetName()})},
	)
	if err != nil {
		msg := fmt.Sprintf("Failed to list Sidecars: %s", err)
		z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
		return err
	}

	zoneNamespaces := make(map[string]struct{}, len(z.Spec.Namespaces))
	for _, ns := range z.Spec.Namespaces {
		zoneNamespaces[ns] = struct{}{}
	}

	additionalSidecarNames := make(map[string]struct{}, len(z.Spec.AdditionalEgress))
	for _, ae := range z.Spec.AdditionalEgress {
		workloadSelectorHash, err := hashWorkloadSelector(ae.WorkloadSelector)
		if err != nil {
			msg := fmt.Sprintf("Failed to hash workload selector: %s", err)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
			return err
		}
		additionalSidecarNames[getSidecarNameFromHash(workloadSelectorHash)] = struct{}{}
	}

	for _, sidecar := range sidecarList.Items {
		if shouldSidecarBeDeleted(sidecar, zoneNamespaces, additionalSidecarNames) {
			log.Info(fmt.Sprintf("Deleting Sidecar %s in namespace %s", sidecar.Name, sidecar.Namespace))
			err = r.Delete(ctx, sidecar)
			if err != nil {
				msg := fmt.Sprintf("Failed to delete Sidecar %s in namespace %s: %s", sidecar.GetName(), sidecar.GetNamespace(), err)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
				return err
			}
		}
	}
	return nil
}

func (r *ZoneReconciler) constructSidecar(z *v1alpha1.Zone, sidecarKey types.NamespacedName, getSidecarSpec func() istioapinetworkingv1.Sidecar) *istioclientnetworkingv1.Sidecar {
	sidecar := &istioclientnetworkingv1.Sidecar{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sidecarKey.Name,
			Namespace: sidecarKey.Namespace,
			Labels:    map[string]string{constants.ZoneLabel: z.GetName()},
		},
		Spec: getSidecarSpec(),
	}
	controllerutil.SetControllerReference(z, sidecar, r.Scheme)

	return sidecar
}

func getSidecarNameFromHash(hash uint32) string {
	return fmt.Sprintf("%s%d", constants.ZoneEgressPrefix, hash)
}

func getZoneHosts(zoneNamespaces []string) []string {
	zoneHosts := make([]string, len(zoneNamespaces))
	for i, ns := range zoneNamespaces {
		zoneHosts[i] = fmt.Sprintf("%s/*", ns)
	}
	return zoneHosts
}

func hashWorkloadSelector(workloadSelector map[string]string) (uint32, error) {
	hash := fnv.New32()
	b, err := json.Marshal(workloadSelector)
	if err != nil {
		return 0, err
	}
	_, err = hash.Write(b)
	if err != nil {
		return 0, err
	}
	return hash.Sum32(), nil
}

func shouldSidecarBeDeleted(sidecar *istioclientnetworkingv1.Sidecar, zoneNamespaces, additionalSidecarNames map[string]struct{}) bool {
	if _, exists := zoneNamespaces[sidecar.Namespace]; !exists {
		return true
	}
	if sidecar.GetName() == constants.SingeltonResourceName {
		return false
	}
	if _, exists := additionalSidecarNames[sidecar.GetName()]; !exists {
		return true
	}
	return false
}
