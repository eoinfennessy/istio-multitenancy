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

	istioapisecurityv1 "istio.io/api/security/v1"
	isttioapitypev1beta1 "istio.io/api/type/v1beta1"
	istioclientsecurityv1 "istio.io/client-go/pkg/apis/security/v1"
	"k8s.io/api/core/v1"
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

func (r *ZoneReconciler) reconcileAuthorizationPolicies(ctx context.Context, z *v1alpha1.Zone) error {
	// Check for default AuthorizationPolicy in each Zone namespace and create/update each if necessary
	for _, ns := range z.Spec.Namespaces {
		apKey := types.NamespacedName{Namespace: ns, Name: constants.ZoneAuthorizationPolicyName}
		if err := r.reconcileAuthorizationPolicy(ctx, z, apKey, func() istioapisecurityv1.AuthorizationPolicy {
			return istioapisecurityv1.AuthorizationPolicy{
				Action: istioapisecurityv1.AuthorizationPolicy_ALLOW,
				Rules: []*istioapisecurityv1.Rule{
					{From: []*istioapisecurityv1.Rule_From{
						{Source: &istioapisecurityv1.Source{
							Namespaces: z.Spec.Namespaces,
						}},
					}},
				},
			}
		}); err != nil {
			return err
		}
	}

	// Check AuthorizationPolicy for each ServiceExport and create/update each if necessary
	for _, se := range z.Spec.ServiceExports {
		// Fetch Service associated with ServiceExport
		svc := &v1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Name: se.Name, Namespace: se.Namespace}, svc); err != nil {
			if apierrors.IsNotFound(err) {
				msg := fmt.Sprintf("Service %s in namespace %s not found", se.Name, se.Namespace)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, msg)
				return &pkgerrors.UnreconcilableError{Err: err}
			} else {
				msg := fmt.Sprintf("Error fetching Service %s in namespace %s: %s", se.Name, se.Namespace, err)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
				return err
			}
		}

		apKey := types.NamespacedName{Namespace: se.Namespace, Name: constants.ZoneExportPrefix + se.Name}
		if err := r.reconcileAuthorizationPolicy(ctx, z, apKey, func() istioapisecurityv1.AuthorizationPolicy {
			return istioapisecurityv1.AuthorizationPolicy{
				Action:   istioapisecurityv1.AuthorizationPolicy_ALLOW,
				Selector: &isttioapitypev1beta1.WorkloadSelector{MatchLabels: svc.Spec.Selector},
				Rules: []*istioapisecurityv1.Rule{
					{From: []*istioapisecurityv1.Rule_From{
						{Source: &istioapisecurityv1.Source{
							Namespaces: append(z.Spec.Namespaces, se.ToNamespaces...),
						}},
					}},
				},
			}
		}); err != nil {
			return err
		}
	}

	if err := r.cleanUpAuthorizationPolicies(ctx, z); err != nil {
		return err
	}

	return nil
}

func (r *ZoneReconciler) reconcileAuthorizationPolicy(ctx context.Context, z *v1alpha1.Zone, apKey types.NamespacedName, getAPSpec func() istioapisecurityv1.AuthorizationPolicy) error {
	log := logf.FromContext(ctx)

	ap := &istioclientsecurityv1.AuthorizationPolicy{}
	err := r.Get(ctx, apKey, ap)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			msg := fmt.Sprintf("Error getting AuthorizationPolicy %s in namespace %s: %s", apKey.Name, apKey.Namespace, err)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
			return err
		}

		// AuthorizationPolicy doesn't exist; Create it
		if err := r.Create(ctx, r.constructAuthorizationPolicy(z, apKey, getAPSpec)); err != nil {
			msg := fmt.Sprintf("Error creating AuthorizationPolicy %s in namespace %s: %s", apKey.Name, apKey.Namespace, err)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
			return err
		}
	} else {
		// AuthorizationPolicy exists; Check if it belongs to the Zone
		labelVal, exists := ap.GetLabels()[constants.ZoneLabel]
		if !exists {
			// TODO: Would checking OwnerReferences be more appropriate?
			msg := fmt.Sprintf("AuthorizationPolicy %s in namespace %s is not part of the Zone because it does not have a Zone label", apKey.Name, apKey.Namespace)
			log.Info(msg)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, msg)
			return pkgerrors.NewUnreconcilableError(msg)
		} else if labelVal != z.GetName() {
			msg := fmt.Sprintf("AuthorizationPolicy %s in namespace %s is already included in Zone %s", apKey.Name, apKey.Namespace, labelVal)
			log.Info(msg)
			z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonUnreconcilable, msg)
			return pkgerrors.NewUnreconcilableError(msg)
		}

		// Update AuthorizationPolicies if necessary
		newSpec := getAPSpec()
		if !isAuthorizationPolicySpecEqual(&ap.Spec, &newSpec) {
			copyAuthorizationPolicySpec(&newSpec, &ap.Spec)
			err = r.Update(ctx, ap)
			if err != nil {
				msg := fmt.Sprintf("Error updating AuthorizationPolicy %s in namespace %s: %s", apKey.Name, apKey.Namespace, err)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
				return err
			}
		}
	}
	return nil
}

// cleanUpAuthorizationPolicies deletes AuthorizationPolicies that should no longer be included in the Zone
func (r *ZoneReconciler) cleanUpAuthorizationPolicies(ctx context.Context, z *v1alpha1.Zone) error {
	apList := &istioclientsecurityv1.AuthorizationPolicyList{}
	err := r.List(ctx, apList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{constants.ZoneLabel: z.GetName()})},
	)
	if err != nil {
		msg := fmt.Sprintf("Failed to list AuthorizationPolicies: %s", err)
		z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
		return err
	}

	nsSet := make(map[string]struct{}, len(z.Spec.Namespaces))
	for _, ns := range z.Spec.Namespaces {
		nsSet[ns] = struct{}{}
	}
	serviceExportKeysSet := make(map[types.NamespacedName]struct{}, len(z.Spec.ServiceExports))
	for _, se := range z.Spec.ServiceExports {
		serviceExportKeysSet[types.NamespacedName{Namespace: se.Namespace, Name: constants.ZoneExportPrefix + se.Name}] = struct{}{}
	}
	for _, ap := range apList.Items {
		if shouldAuthorizationPolicyBeDeleted(ap, nsSet, serviceExportKeysSet) {
			err = r.Delete(ctx, ap)
			if err != nil {
				msg := fmt.Sprintf("Failed to delete default AuthorizationPolicy in namespace %s: %s", ap.GetNamespace(), err)
				z.Status.SetStatusCondition(v1alpha1.ConditionTypeReconciled, metav1.ConditionFalse, v1alpha1.ConditionReasonReconcileError, msg)
				return err
			}
		}
	}
	return nil
}

func (r *ZoneReconciler) constructAuthorizationPolicy(z *v1alpha1.Zone, apKey types.NamespacedName, getAPSpec func() istioapisecurityv1.AuthorizationPolicy) *istioclientsecurityv1.AuthorizationPolicy {
	ap := &istioclientsecurityv1.AuthorizationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      apKey.Name,
			Namespace: apKey.Namespace,
			Labels:    map[string]string{constants.ZoneLabel: z.GetName()},
		},
		Spec: getAPSpec(),
	}
	controllerutil.SetControllerReference(z, ap, r.Scheme)

	return ap
}

func shouldAuthorizationPolicyBeDeleted(ap *istioclientsecurityv1.AuthorizationPolicy, nsSet map[string]struct{}, serviceExportKeysSet map[types.NamespacedName]struct{}) bool {
	if _, exists := nsSet[ap.Namespace]; !exists {
		return true
	}
	if ap.Name == constants.ZoneAuthorizationPolicyName {
		return false
	}
	_, exists := serviceExportKeysSet[types.NamespacedName{Namespace: ap.Namespace, Name: ap.Name}]
	return !exists
}

func isAuthorizationPolicySpecEqual(a, b *istioapisecurityv1.AuthorizationPolicy) bool {
	return a.Action == b.Action &&
		reflect.DeepEqual(a.Rules, b.Rules) &&
		reflect.DeepEqual(a.Selector, b.Selector) &&
		reflect.DeepEqual(a.ActionDetail, b.ActionDetail) &&
		reflect.DeepEqual(a.TargetRef, b.TargetRef)
}

func copyAuthorizationPolicySpec(src, dst *istioapisecurityv1.AuthorizationPolicy) {
	dst.Action = src.Action
	dst.Rules = src.Rules
	dst.Selector = src.Selector.DeepCopy()
	dst.ActionDetail = src.ActionDetail
	dst.TargetRef = src.TargetRef.DeepCopy()
}