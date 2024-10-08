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

package constants

const (
	// MetadataNamespace is the namespace for configscoping metadata (labels, annotations)
	MetadataNamespace = "configscoping.istio.eoinfennessy.com"

	// SingeltonResourceName is the name given to resources where only one resource should exist on the cluster or in a namespace
	SingeltonResourceName = "default"

	// Wildcard character
	Wildcard = "*"

	// ZoneAuthorizationPolicyName is the name given to all AuthorizationPolicies in a Zone
	ZoneAuthorizationPolicyName = "allow-zone"

	// ZoneEgressPrefix is a prefix added to the name of Sidecars targeting specific workloads in the Zone.
	ZoneEgressPrefix = "zone-egress-"

	// ZoneExportPrefix is a prefix added to the name of resources that enable a Service to be exported from the Zone
	ZoneExportPrefix = "zone-export-"

	// ZoneFinalizer is the finalizer name the controller adds to Zones that need to be finalized during deletion
	ZoneFinalizer = MetadataNamespace + "/zone-finalizer"

	// ZoneLabel is the label indicating the Zone that a resource is currently included in
	ZoneLabel = MetadataNamespace + "/zone"
)
