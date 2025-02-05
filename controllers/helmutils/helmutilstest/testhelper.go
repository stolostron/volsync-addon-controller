package helmutilstest

import (
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
)

//nolint:funlen
func VerifyHelmRenderedVolSyncObjects(objs []runtime.Object,
	testNamespace string, clusterIsOpenShift bool) *appsv1.Deployment {
	// Check objects
	// There should be:
	// - 2 CRDs (replicationsource, replicationdestination)
	// - 3 clusterroles
	//   - 1 for the manager controller
	//   - 1 for metrics reader
	//   - 1 for proxy
	// - 2 clusterrolebindings
	//   - 1 for the manager controller
	//   - 1 for proxy
	// - 1 role (leader election)
	// - 1 rolebinding (leader election)
	// - 1 deployment (volsync)
	// - 1 service (metrics)
	// - 1 serviceaccount (volsync)
	Expect(len(objs)).To(Equal(12))

	crds := []*apiextensionsv1.CustomResourceDefinition{}
	clusterRoles := []*rbacv1.ClusterRole{}
	clusterRoleBindings := []*rbacv1.ClusterRoleBinding{}
	var role *rbacv1.Role
	var roleBinding *rbacv1.RoleBinding
	var deployment *appsv1.Deployment
	var service *corev1.Service
	var serviceAccount *corev1.ServiceAccount

	for _, obj := range objs {
		objKind := obj.GetObjectKind().GroupVersionKind().Kind

		klog.InfoS("Object kind", "objKind", objKind)

		switch objKind {
		case "CustomResourceDefinition":
			crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
			Expect(ok).To(BeTrue())
			crds = append(crds, crd)
		case "ClusterRole":
			clusterRole, ok := obj.(*rbacv1.ClusterRole)
			Expect(ok).To(BeTrue())
			clusterRoles = append(clusterRoles, clusterRole)
		case "ClusterRoleBinding":
			clusterRoleBinding, ok := obj.(*rbacv1.ClusterRoleBinding)
			Expect(ok).To(BeTrue())
			clusterRoleBindings = append(clusterRoleBindings, clusterRoleBinding)
		case "Role":
			r, ok := obj.(*rbacv1.Role)
			Expect(ok).To(BeTrue())
			role = r
		case "RoleBinding":
			rb, ok := obj.(*rbacv1.RoleBinding)
			Expect(ok).To(BeTrue())
			roleBinding = rb
		case "Deployment":
			d, ok := obj.(*appsv1.Deployment)
			Expect(ok).To(BeTrue())
			deployment = d
		case "Service":
			s, ok := obj.(*corev1.Service)
			Expect(ok).To(BeTrue())
			service = s
		case "ServiceAccount":
			sa, ok := obj.(*corev1.ServiceAccount)
			Expect(ok).To(BeTrue())
			serviceAccount = sa
		}
	}

	Expect(len(crds)).To(Equal(2))
	Expect(len(clusterRoles)).To(Equal(3))
	Expect(len(clusterRoleBindings)).To(Equal(2))
	Expect(role).NotTo(BeNil())
	Expect(roleBinding).NotTo(BeNil())
	Expect(deployment).NotTo(BeNil())
	Expect(service).NotTo(BeNil())
	Expect(serviceAccount).NotTo(BeNil())

	// Check CRDs
	foundReplicationSourceCRD := false
	foundReplicationDestinationCRD := false
	for _, crd := range crds {
		if crd.GetName() == "replicationsources.volsync.backube" {
			foundReplicationSourceCRD = true
		} else if crd.GetName() == "replicationdestinations.volsync.backube" {
			foundReplicationDestinationCRD = true
		}
	}
	Expect(foundReplicationSourceCRD).To(BeTrue())
	Expect(foundReplicationDestinationCRD).To(BeTrue())

	// Check namespace on namespaced resources is set correctly
	namespacedObjs := []metav1.Object{
		role,
		roleBinding,
		deployment,
		service,
		serviceAccount,
	}
	for _, nsObj := range namespacedObjs {
		Expect(nsObj.GetNamespace()).To(Equal(testNamespace))
	}

	// Check deployment
	Expect(deployment.GetName()).To(Equal("volsync"))
	Expect(len(deployment.Spec.Template.Spec.Containers)).To(Equal(2))
	Expect(deployment.Spec.Template.Spec.Containers[0].Name).To(Equal("kube-rbac-proxy"))
	Expect(deployment.Spec.Template.Spec.Containers[1].Name).To(Equal("manager"))

	Expect(deployment.Spec.Template.Spec.ServiceAccountName).To(Equal(serviceAccount.GetName()))
	if clusterIsOpenShift {
		// RunAsUser should not be set for OpenShift, openshift will set this
		Expect(deployment.Spec.Template.Spec.SecurityContext.RunAsUser).To(BeNil())
	} else {
		Expect(deployment.Spec.Template.Spec.SecurityContext.RunAsUser).NotTo(BeNil())
		Expect(*deployment.Spec.Template.Spec.SecurityContext.RunAsUser).To(Equal(int64(65534)))
	}

	// Return the deployment (can be checked further by tests)
	return deployment
}
