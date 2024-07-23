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
	"fmt"
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

	"github.com/eoinfennessy/istio-multitenancy/api/v1alpha1"
	"github.com/eoinfennessy/istio-multitenancy/pkg/constants"
	pkgerrors "github.com/eoinfennessy/istio-multitenancy/pkg/errors"
)

func (r *ZoneReconciler) reconcileSidecars(ctx context.Context, z *v1alpha1.Zone) error {
	log := logf.FromContext(ctx)

	// Check for Sidecar in each Zone namespace and create/update each if necessary
	for _, ns := range z.Spec.Namespaces {
		sidecar := &istioclientnetworkingv1.Sidecar{}
		err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: constants.SingeltonResourceName}, sidecar)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				msg := fmt.Sprintf("Error getting default sidecar in namespace %s: %s", ns, err)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
				return err
			}

			// Sidecar doesn't exist; Create it
			if err := r.Create(ctx, r.constructSidecar(z, ns)); err != nil {
				msg := fmt.Sprintf("Error creating default sidecar in namespace %s: %s", ns, err)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
				return err
			}
		} else {
			// Sidecar exists; Check if it belongs to the Zone
			labelVal, exists := sidecar.GetLabels()[constants.ZoneLabel]
			if !exists {
				// TODO: Would checking OwnerReferences be more appropriate?
				msg := fmt.Sprintf("default Sidecar in namespace %s is not part of the Zone because it does not have a Zone label", ns)
				log.Info(msg)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, msg)
				return pkgerrors.NewUnreconcilableError(msg)
			} else if labelVal != z.GetName() {
				msg := fmt.Sprintf("default Sidecar in namespace %s is already included in Zone %s", ns, labelVal)
				log.Info(msg)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, msg)
				return pkgerrors.NewUnreconcilableError(msg)
			}

			// Update Sidecar if necessary
			newSpec := constructSidecarSpec(z)
			if !reflect.DeepEqual(sidecar.Spec.Egress, newSpec.Egress) {
				sidecar.Spec.Egress = newSpec.Egress
				err = r.Update(ctx, sidecar)
				if err != nil {
					msg := fmt.Sprintf("Error updating default Sidecar in namespace %s: %s", ns, err)
					z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
					return err
				}
			}
		}
	}

	if err := r.cleanUpSidecars(ctx, z); err != nil {
		return err
	}

	return nil
}

// cleanUpSidecars deletes Sidecars that should no longer be included in the Zone
func (r *ZoneReconciler) cleanUpSidecars(ctx context.Context, z *v1alpha1.Zone) error {
	sidecarList := &istioclientnetworkingv1.SidecarList{}
	err := r.List(ctx, sidecarList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{constants.ZoneLabel: z.GetName()})},
	)
	if err != nil {
		msg := fmt.Sprintf("Failed to list Sidecars: %s", err)
		z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
		return err
	}

	nsSet := make(map[string]struct{}, len(z.Spec.Namespaces))
	for _, ns := range z.Spec.Namespaces {
		nsSet[ns] = struct{}{}
	}
	for _, sidecar := range sidecarList.Items {
		if _, exists := nsSet[sidecar.GetNamespace()]; !exists {
			err = r.Delete(ctx, sidecar)
			if err != nil {
				msg := fmt.Sprintf("Failed to delete default Sidecar in namespace %s: %s", sidecar.GetNamespace(), err)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
				return err
			}
		}
	}
	return nil
}

func (r *ZoneReconciler) constructSidecar(z *v1alpha1.Zone, namespace string) *istioclientnetworkingv1.Sidecar {
	sidecar := &istioclientnetworkingv1.Sidecar{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.SingeltonResourceName,
			Namespace: namespace,
			Labels:    map[string]string{constants.ZoneLabel: z.GetName()},
		},
		Spec: constructSidecarSpec(z),
	}
	controllerutil.SetControllerReference(z, sidecar, r.Scheme)

	return sidecar
}

func constructSidecarSpec(z *v1alpha1.Zone) istioapinetworkingv1.Sidecar {
	hosts := make([]string, len(z.Spec.Namespaces))
	for i, ns := range z.Spec.Namespaces {
		hosts[i] = fmt.Sprintf("%s/*", ns)
	}

	return istioapinetworkingv1.Sidecar{
		Egress: []*istioapinetworkingv1.IstioEgressListener{
			{Hosts: hosts},
		},
	}
}
