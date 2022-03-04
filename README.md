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
$ oc label managedcluster my-managed-cluster addons.open-cluster-management.io/volsync="true"
```

# Development

## Installation

To install manually, helm charts are available [here](https://github.com/stolostron/volsync-addon-controller-chart)

## Running the controller locally pointing to a remote cluster

If you would like to run the volsync addon controller outside the cluster, execute:

```shell
$ go run . controller --namespace <namesapce> --kubeconfig <path_to_kubeconfig>
```
