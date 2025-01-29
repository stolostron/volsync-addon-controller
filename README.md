# volsync-addon-controller

An addon controller to be installed with Red Hat Advanced Cluster Management.  The addon controller will
look for ManagedClusterAddOn CRs for managed cluster and then deploy the volsync operator on those managed
clusters.

## License

This project is licensed under the *Apache License 2.0*. A copy of the license can be found in [LICENSE](LICENSE).

## Installing

This product will be installed automatically with Red Hat Advanced Cluster Management.

## Usage

On a hub cluster with the volsync addon controller running, create a ManagedClusterAddOn in the namespace
of the managed cluster you want the volsync operator installed on.

Sample ManagedClusterAddOn (replace `managed_cluster_namespace` with the appropriate managed cluster name):

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: volsync
  namespace: <managed_cluster_namespace>
spec: {}
```

### Advanced usage

Optional annotations can be added to override the defaults used by the ACM operator.

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: volsync
  namespace: <managed_cluster_namespace>
  annotations:
    operator-subscription-channel: "stable"
    operator-subscription-source: "custom-catalog-source"
    operator-subscription-sourceNamespace: "openshift-marketplace"
    operator-subscription-installPlanApproval: "Manual"
    operator-subscription-startingCSV: "volsync.vX.Y.Z"
spec: {}
```

### Installing via a label on a managed cluster

Instead of manually creating a ManagedClusterAddOn CR, you can alternatively install VolSync on your managed
clusters by adding a label to the ManagedCluster resource on the hub cluster.

If the label `addons.open-cluster-management.io/volsync` is set to value "true" on a ManagedCluster resource on the hub
then the addon controller will automatically create a ManagedClusterAddOn in the namespace for the managed cluster and
thus trigger the deployment of the volsync operator on that managed cluster.

Example using the `oc` command to add the label to a managed cluster.

```shell
oc label managedcluster my-managed-cluster addons.open-cluster-management.io/volsync="true"
```

## Testing using the downstream operator catalog

To test the addon controller and VolSync operator for builds that are not yet published to the official Red Hat
Catalog (for example prior to our initial VolSync operator release, or when new pre-release versions are
published to the downstream catalog), testers can follow the following steps.

These steps assume that there is a hub cluster with ACM installed with the version of the volsync-addon-controller
you want to test with.  It also assumes there is 1 or more managed clusters, and these managed clusters are the ones
that you want to deploy the VolSync operator on.

### On the managed clusters

- First disable old catalog sources, see [deploy-from-brew - Step 0 disable old catalogsources](https://github.com/stolostron/deploy/blob/master/docs/deploy-from-brew.md#step-0-disable-old-catalogsources).
  This step is so we can stop using the prebuilt operator catalog and instead replace with the downstream pre-release
  catalog

  ```shell
  oc patch OperatorHub cluster --type json -p '[{"op": "add", "path": "/spec/disableAllDefaultSources","value": true}]'
  ```

- Follow [deploy-from-brew](https://github.com/stolostron/deploy/blob/master/docs/deploy-from-brew.md)
  steps `0, 1 & 2`.  These steps involve authenticating so the downstream image can be pulled, updating the auth on the
  cluster itself and also creating an ImageContentSourcePolicy (ICSP) to add brew as a mirror for requests to the
  redhat registry.

- Create a catalog source to point to the image.  This will essentially look like the official `redhat-operators`
  catalog source.  The image should be the image that corresponds to the downstream operator bundle image you want
  to test against.

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: volsync-operators
  namespace: openshift-marketplace
spec:
  sourceType: grpc
  image: <REPLACE_IMAGE>
  displayName: VolSync Operator Catalog
  publisher: grpc
```

**REPLACE_IMAGE** should be replaced with the image.

As an example, you may have an index image location for the build that looks like this:

```text
Index image v4.6: registry-proxy.engineering.redhat.com/rh-osbs/iib:199520
Index image v4.7: registry-proxy.engineering.redhat.com/rh-osbs/iib:199545
Index image v4.8: registry-proxy.engineering.redhat.com/rh-osbs/iib:199592
Index image v4.9: registry-proxy.engineering.redhat.com/rh-osbs/iib:199646
Index image v4.10: registry-proxy.engineering.redhat.com/rh-osbs/iib:199693
Index image v4.11: registry-proxy.engineering.redhat.com/rh-osbs/iib:199736
```

If you are testing on an OCP 4.9 image, you would then choose the
`registry-proxy.engineering.redhat.com/rh-osbs/iib:199646` image.

You will also need to replace `registry-proxy.engineering.redhat.com` with `brew.registry.redhat.io` in the image path.

So `registry-proxy.engineering.redhat.com/rh-osbs/iib:199646` becomes `brew.registry.redhat.io/rh-osbs/iib:199646`

Example edited CatalogSource:

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: volsync-operators
  namespace: openshift-marketplace
spec:
  sourceType: grpc
  image: brew.registry.redhat.io/rh-osbs/iib:199646`
  displayName: VolSync Operator Catalog
  publisher: grpc
```

### On the Hub cluster

- Confirm the volsync-addon-controller is running in the `open-cluster-management` namespace (it should be
    deployed as part of ACM)

- Import the managed cluster(s) you want to test

To Deploy VolSync on managed clusters, you can either create a ManagedClusterAddOn or add a label to the
ManagedCluster resource on the hub.

### Deploying VolSync to ManagedCluster via label (to be done on the hub cluster)

Add the label: `addons.open-cluster-management.io/volsync` with value `"true"`

For example to add the label to a managed cluster named `test-managed-1` you can do the following:

```shell
oc label managedcluster test-managed-1 "addons.open-cluster-management.io/volsync"="true"
```

After this step you should see a ManagedClusterAddOn resource should be created automatically for you on the hub
in the namespace for the managed cluster.

### Deploying VolSync to ManagedCluster via ManagedClusterAddOn (to be done on the hub cluster)

Alternatively, you can create the ManagedClusterAddOn resource yourself.

Create a ManagedClusterAddOn:

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: volsync
  namespace: <MANAGED_CLUSTER_NAMESPACE>
spec: {}
```

Replace `MANAGED_CLUSTER_NAMESPACE` with the namespace of your managed cluster (this is the same as the managed cluster
name)

## Setting nodeSelectors or tolerations in the volsync deploys on managed clusters

This is part of [story](https://github.com/stolostron/backlog/issues/26712).  Also see the dev
guide [here](https://docs.google.com/document/d/1MkYz3RKjU67vn5qXCDCQ5i3eAxdLUbaTq1mCw3ObV3I/edit#)

To use node selectors/tolerations, a ClusterManagementAddOn for VolSync should exist on the hub.  Note that this is
now included in the volsync-addon-controller-charts - so there should be one deployed by default when deploying ACM.

The ClusterManagementAddOn can be edited to indicate that the addon can refer to a AddonDeploymentConfig.  The
AddonDeploymentConfig CR is the resource where node selectors and tolerations can be specified.

Edit the volsync ClusterManagementAddOn and add supportedConfigs section to add `addondeploymentconfigs`:

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: volsync
spec:
  addOnMeta:
    displayName: VolSync
    description: VolSync
  supportedConfigs:
  - group: addon.open-cluster-management.io
    resource: addondeploymentconfigs
```

Now the addonDeploymentConfig can be created on the hub, here is an example:

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: AddOnDeploymentConfig
metadata:
  name: volsync-addondeployconfig
  namespace: default
spec:
  nodePlacement:
    nodeSelector:
      ttest: volsync
    tolerations:
    - effect: NoSchedule
      key: node.kubernetes.io/unreachable
      operator: Exists
```

To actually tell the volsync deploy (via the operator subscription) to use the addonDeploymentConfig, it can either
be specified as a default in the `ClusterManagementAddOn` or it can be specified on an individual deploy basis
by setting it in the `ManagedClusterAddOn`.

To set on a ManagedClusterAdddon, you can do it like this on the hub:

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: volsync
  namespace: cluster1
spec:
  configs:
  - group: addon.open-cluster-management.io
    resource: addondeploymentconfigs
    name: volsync-addondeployconfig
    namespace: default
```

If instead you want to set this as the default for any VolSync ManagedClusterAddOn then you can set it as the default
in the ClusterManagementAddon on the hub.  Note that the default can be overridden by specifying the
addondeploymentconfig in the ManagedClusterAddOn individually (as above).

Example of setting a default in the ClusterManagementAddOn (on the hub):

```yaml
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ClusterManagementAddOn
metadata:
  name: volsync
spec:
  addOnMeta:
    displayName: VolSync
    description: VolSync
  supportedConfigs:
  - group: addon.open-cluster-management.io
    resource: addondeploymentconfigs
    defaultConfig:
      name: volsync-addondeployconfig
      namespace: default
```

## Development

## Installation

To install manually, helm charts are available [here](https://github.com/stolostron/multiclusterhub-operator/tree/main/pkg/templates/charts/toggle/volsync-controller)

## Running the controller locally pointing to a remote cluster

If you would like to run the volsync addon controller outside the cluster, execute:

```shell
make run
```
