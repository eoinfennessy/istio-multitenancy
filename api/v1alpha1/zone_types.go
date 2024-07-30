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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceExport defines a list of namespaces outside the Zone to export the specified Service to.
type ServiceExport struct {
	// Name is the name of the Service to be exported.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the Service to be exported.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// ToNamespaces is a list of the namespaces the Service will be exported to. Use
	// the wildcard ["*"] to export the service to the entire mesh.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	ToNamespaces []string `json:"toNamespaces"`
}

// AdditionalEgress defines additional service hosts outside the Zone that will
// be exposed to Zone workloads matching the WorkloadSelector.
type AdditionalEgress struct {
	// WorkloadSelector is a map of labels used to select the workloads that will
	// include the additional egress hosts. The targeted workloads must match all
	// labels specified in WorkloadSelector.
	// +kubebuilder:validation:Required
	WorkloadSelector map[string]string `json:"workloadSelector"`

	// Hosts is a list of additional service hosts that will be exposed to the
	// specified workloads. Each host follows the <namespace>/<dns_name> format, and
	// the wildcard "*" can be specified for both the namespace and (part of) the DNS
	// name.
	// See https://istio.io/latest/docs/reference/config/networking/sidecar/#IstioEgressListener for more info.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Hosts []string `json:"hosts"`
}

// ZoneSpec defines the desired state of Zone
type ZoneSpec struct {
	// Namespaces is a list of namespaces to include in the Zone
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Namespaces []string `json:"namespaces"`

	// ServiceExports can optionally be defined to export Services to namespaces outside the Zone.
	ServiceExports []ServiceExport `json:"serviceExports,omitempty"`

	// AdditionalEgress can optionally be defined to allow additional service hosts
	// outside the Zone to be exposed to workloads that are part of the Zone.
	AdditionalEgress []AdditionalEgress `json:"additionalEgress,omitempty"`
}

// ZoneStatus defines the observed state of Zone
type ZoneStatus struct {
	// Represents the latest available observations of the object's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// FindStatusCondition finds the conditionType in the ZoneStatus conditions.
func (s *ZoneStatus) FindStatusCondition(conditionType ZoneConditionType) *metav1.Condition {
	return meta.FindStatusCondition(s.Conditions, string(conditionType))
}

// SetStatusCondition sets the corresponding condition in conditions to newCondition and returns true
// if the conditions are changed by this call.
// conditions must be non-nil.
//  1. if the condition of the specified type already exists (all fields of the existing condition are updated to
//     newCondition, LastTransitionTime is set to now if the new status differs from the old status)
//  2. if a condition of the specified type does not exist (LastTransitionTime is set to now() if unset, and newCondition is appended)
func (s *ZoneStatus) SetStatusCondition(conditionType ZoneConditionType, status metav1.ConditionStatus, reason ZoneConditionReason, message string) bool {
	return meta.SetStatusCondition(
		&s.Conditions,
		metav1.Condition{
			Type:    string(conditionType),
			Status:  status,
			Reason:  string(reason),
			Message: message,
		},
	)
}

// ZoneConditionType represents the type of the Zone's condition.
// Zone condition types are: Reconciled
type ZoneConditionType string

// ZoneConditionReason indicates why the condition type is in its current state
type ZoneConditionReason string

const (
	// ConditionTypeReconciled signifies whether the controller has successfully
	// reconciled resources associated with a Zone.
	ConditionTypeReconciled ZoneConditionType = "Reconciled"

	// ConditionReasonReconcileInProgress indicates that the reconciliation
	// of the Zone is progressing.
	ConditionReasonReconcileInProgress ZoneConditionReason = "ReconcileInProgress"

	// ConditionReasonReconcileError indicates that the reconciliation
	// of the Zone has failed and will be retried.
	ConditionReasonReconcileError ZoneConditionReason = "ReconcileError"

	// ConditionReasonUnreconcilable indicates that the Zone cannot currently be reconciled, and
	// reconciliation will not be retried.
	ConditionReasonUnreconcilable ZoneConditionReason = "Unreconcilable"

	// ConditionReasonReconcileSuccess indicates that the reconciliation
	// of the Zone has succeeded.
	ConditionReasonReconcileSuccess ZoneConditionReason = "ReconcileSuccess"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// Zone is the Schema for the zones API
type Zone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZoneSpec   `json:"spec,omitempty"`
	Status ZoneStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ZoneList contains a list of Zone
type ZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Zone `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Zone{}, &ZoneList{})
}
