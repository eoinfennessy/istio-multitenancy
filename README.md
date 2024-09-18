# istio-zones

A Kubernetes controller and CRD providing a high-level API for managing the configuration scope of Istio's data plane.

The API allows the mesh to be divided into "Zones" (sets of namespaces).
The visibility of service producers (K8s Services) within a Zone is limited to sidecar proxies in Zone namespaces,
and the scope of sidecar proxies for service consumers in a Zone is limited to hosts in Zone namespaces.
Additionally, access to services in the Zone is allowed only from workloads in Zone namespaces.

The Zone API also makes it possible to export Services in a Zone to namespaces outside the Zone,
allowing specified Services to be visible to proxies in the specified namespaces.
The scope of some workloads in a Zone can also be extended to include specific hosts outside the Zone.

## Motivation

By default, the Istio control plane maintains information about all cluster workloads in each proxy's config.
This becomes a scalability issue for large clusters containing many workloads and proxies
because of the cost of CPU and memory usage required to maintain this config for all sidecar proxies.

Istio currently offers [ways to manage config scoping in a mesh](https://istio.io/latest/docs/ops/configuration/mesh/configuration-scoping/),
including Sidecar resources, `exportTo` annotations on K8s Services,
and `exportTo` functionality for ServiceEntries, VirtualServices, and DestinationRules.

Managing config scoping in this way can pose some challenges in large meshes.
It introduces a significant quantity of additional configuration that must be maintained across multiple resources
(Sidecar resources targeting workloads in each namespace, `exportTo` annotations on Services, etc.).
This config needs to be carefully updated to reflect changes to each team's requirements,
which can lead to issues where resources are not updated or configured appropriately.

The Zone API allows the config scope of all resources in a set of namespaces to be managed using a single resource.
Using Zones in this way makes it easier to configure config scoping for the entire mesh,
and ensures that the config scope is updated appropriately
as Services are added to each Zone, or when a Zone's namespaces get updated.
Beyond config scoping, each Zone resource can also be used to limit access to Zone workloads
by only allowing requests from workloads in the same Zone.

A typical use case for the Zone API might involve an infrastructure team responsible for managing the mesh control plane,
and multiple (largely independent) application teams.
The infrastructure team creates and maintains a Zone for each application team,
and each application team can use their scoped down section of the mesh
without needing to concern themselves with config scoping as they add services in their Zone.
If an application team has a requirement to expose one of their Services to another team's namespace,
or extend the egress scope of one of their workloads beyond the Zone,
the infrastructure team can manage this using the same Zone resource that defines the application team's namespaces.

## Usage

### Installation

After installing Istio, run the following to install the latest Zone CRD and controller.

```shell
kubectl apply -k "https://github.com/openshift-service-mesh/istio-zones//config/default/?version=main"
```

The `version` can be changed to any branch or tag (e.g. `v0.2.0`).

### Creating Zones

Zones can be created after the controller deployment is available.
Below is a simple Zone that limits defines the config scope and access for two namespaces.

```yaml
apiVersion: configscoping.istio.eoinfennessy.com/v1alpha1
kind: Zone
metadata:
  name: blue
spec:
  namespaces:
    - blue-a
    - blue-b
```

It's recommended to [read the example usage doc](https://github.com/openshift-service-mesh/istio-zones/blob/main/docs/example.md) to learn about additional capabilities of the Zone API,
including how to export Services in a Zone to namespaces outside the Zone,
how to increase the egress scope of workloads in the Zone,
and how AuthorizationPolicy management works with the Zone API. 

## Development

### Prerequisites
- go version v1.22.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/istio-zones:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/istio-zones:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following are the steps to build the installer and distribute this project to users.

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/istio-zones:tag
```

NOTE: The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without
its dependencies.

2. Using the installer

Users can just run kubectl apply -f <URL for YAML BUNDLE> to install the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/istio-zones/<tag or branch>/dist/install.yaml
```

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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

