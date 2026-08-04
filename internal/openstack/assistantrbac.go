/*
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

package openstack

import (
	"context"
	"fmt"

	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	common_rbac "github.com/openstack-k8s-operators/lib-common/modules/common/rbac"
	corev1 "github.com/openstack-k8s-operators/openstack-operator/api/core/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// assistantServiceAccountName is the fixed, well-known name of the
// ServiceAccount that runs the OpenStackAssistant pod. OpenStackAssistant is
// reconciled by lightspeed-operator (not this operator); it sets its pod's
// ServiceAccountName to this exact value to inherit the read-only permission
// set granted here. Keep in sync with
// lightspeed-operator/api/v1beta1.OpenStackAssistantServiceAccountName.
const assistantServiceAccountName = "openstackassistant"

// assistantRBACReconciler adapts an OpenStackControlPlane instance to
// common_rbac.Reconciler, so that common_rbac.ReconcileRbac creates the
// assistant's ServiceAccount/Role/RoleBinding under the fixed name above
// (once per namespace) rather than a name derived from a per-CR instance.
type assistantRBACReconciler struct {
	instance *corev1.OpenStackControlPlane
}

func (a assistantRBACReconciler) RbacConditionsSet(c *condition.Condition) {
	a.instance.Status.Conditions.Set(c)
}

func (a assistantRBACReconciler) RbacNamespace() string {
	return a.instance.Namespace
}

func (a assistantRBACReconciler) RbacResourceName() string {
	return assistantServiceAccountName
}

// assistantClusterRoleName returns the cluster-scoped ClusterRole/
// ClusterRoleBinding name for the given namespace. Cluster-scoped RBAC
// objects must be uniquely named across namespaces.
func assistantClusterRoleName(namespace string) string {
	return fmt.Sprintf("%s-%s", assistantServiceAccountName, namespace)
}

// ReconcileAssistantRBAC creates the OpenStackAssistant RBAC identity: a
// namespace-scoped ServiceAccount/Role/RoleBinding (read-only access to
// OpenStack and supporting resources within the namespace) plus a
// cluster-scoped ClusterRole/ClusterRoleBinding (read-only access to
// genuinely cluster-scoped resources such as nodes and events). This is
// created once per OpenStackControlPlane namespace under a fixed name, so
// that a same-namespace OpenStackAssistant pod - reconciled by
// lightspeed-operator - can run under it without lightspeed-operator needing
// any RBAC-management permissions of its own.
func ReconcileAssistantRBAC(ctx context.Context, instance *corev1.OpenStackControlPlane, helper *helper.Helper) (ctrl.Result, error) {
	reconciler := assistantRBACReconciler{instance: instance}

	rbacResult, err := common_rbac.ReconcileRbac(ctx, helper, reconciler, assistantNamespacedRbacRules())
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			corev1.OpenStackControlPlaneAssistantRBACReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			corev1.OpenStackControlPlaneAssistantRBACReadyErrorMessage,
			err.Error()))
		return rbacResult, err
	} else if (rbacResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			corev1.OpenStackControlPlaneAssistantRBACReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			corev1.OpenStackControlPlaneAssistantRBACReadyInitMessage))
		return rbacResult, nil
	}

	clusterRoleName := assistantClusterRoleName(instance.Namespace)

	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterRoleName,
		},
	}
	_, err = controllerutil.CreateOrPatch(ctx, helper.GetClient(), clusterRole, func() error {
		clusterRole.Rules = assistantClusterRoleRules()
		return nil
	})
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			corev1.OpenStackControlPlaneAssistantRBACReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			corev1.OpenStackControlPlaneAssistantRBACReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("error reconciling assistant ClusterRole %s: %w", clusterRoleName, err)
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterRoleName,
		},
	}
	_, err = controllerutil.CreateOrPatch(ctx, helper.GetClient(), clusterRoleBinding, func() error {
		clusterRoleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     clusterRoleName,
		}
		clusterRoleBinding.Subjects = []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      assistantServiceAccountName,
			Namespace: instance.Namespace,
		}}
		return nil
	})
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			corev1.OpenStackControlPlaneAssistantRBACReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			corev1.OpenStackControlPlaneAssistantRBACReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("error reconciling assistant ClusterRoleBinding %s: %w", clusterRoleName, err)
	}

	instance.Status.Conditions.MarkTrue(
		corev1.OpenStackControlPlaneAssistantRBACReadyCondition,
		corev1.OpenStackControlPlaneAssistantRBACReadyMessage)

	return ctrl.Result{}, nil
}

// DeleteAssistantRBAC removes the cluster-scoped ClusterRole/ClusterRoleBinding
// created by ReconcileAssistantRBAC. It's called from OpenStackControlPlane's
// delete path since these objects can't use owner references (cluster-scoped,
// can't be owned by a namespaced resource). The namespace-scoped
// ServiceAccount/Role/RoleBinding are owned by the OpenStackControlPlane and
// are garbage-collected automatically.
func DeleteAssistantRBAC(ctx context.Context, instance *corev1.OpenStackControlPlane, helper *helper.Helper) error {
	name := assistantClusterRoleName(instance.Namespace)

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if err := helper.GetClient().Delete(ctx, clusterRoleBinding); err != nil && !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("error deleting assistant ClusterRoleBinding %s: %w", name, err)
	}

	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	if err := helper.GetClient().Delete(ctx, clusterRole); err != nil && !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("error deleting assistant ClusterRole %s: %w", name, err)
	}

	return nil
}

func assistantNamespacedRbacRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{
				"pods", "pods/log", "services", "endpoints",
				"configmaps", "secrets", "events",
				"persistentvolumeclaims", "serviceaccounts",
			},
			Verbs: []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments", "statefulsets", "daemonsets", "replicasets"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"batch"},
			Resources: []string{"jobs", "cronjobs"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"route.openshift.io"},
			Resources: []string{"routes"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"k8s.cni.cncf.io"},
			Resources: []string{"network-attachment-definitions"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"cert-manager.io"},
			Resources: []string{"certificates", "issuers"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{
				"core.openstack.org",
				"dataplane.openstack.org",
				"keystone.openstack.org",
				"mariadb.openstack.org",
				"memcached.openstack.org",
				"rabbitmq.openstack.org",
				"nova.openstack.org",
				"neutron.openstack.org",
				"glance.openstack.org",
				"cinder.openstack.org",
				"heat.openstack.org",
				"octavia.openstack.org",
				"designate.openstack.org",
				"barbican.openstack.org",
				"manila.openstack.org",
				"horizon.openstack.org",
				"swift.openstack.org",
				"placement.openstack.org",
				"ovn.openstack.org",
				"ironic.openstack.org",
				"telemetry.openstack.org",
				"network.openstack.org",
			},
			Resources: []string{"*"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
}

func assistantClusterRoleRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"nodes", "persistentvolumes", "namespaces"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"config.openshift.io"},
			Resources: []string{"clusteroperators", "clusterversions"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"nmstate.io"},
			Resources: []string{"nodenetworkconfigurationpolicies"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"machine.openshift.io"},
			Resources: []string{"machines", "machinesets"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"storage.k8s.io"},
			Resources: []string{"storageclasses"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			NonResourceURLs: []string{"/ls-access"},
			Verbs:           []string{"get"},
		},
	}
}
