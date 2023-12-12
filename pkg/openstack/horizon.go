package openstack

import (
	"context"
	"fmt"

	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	horizonv1 "github.com/openstack-k8s-operators/horizon-operator/api/v1beta1"
	corev1beta1 "github.com/openstack-k8s-operators/openstack-operator/apis/core/v1beta1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
)

// ReconcileHorizon -
func ReconcileHorizon(ctx context.Context, instance *corev1beta1.OpenStackControlPlane, version *corev1beta1.OpenStackVersion, helper *helper.Helper) (ctrl.Result, error) {
	horizon := &horizonv1.Horizon{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "horizon",
			Namespace: instance.Namespace,
		},
	}
	Log := GetLogger(ctx)

	if !instance.Spec.Horizon.Enabled {
		if res, err := EnsureDeleted(ctx, helper, horizon); err != nil {
			return res, err
		}
		instance.Status.Conditions.Remove(corev1beta1.OpenStackControlPlaneHorizonReadyCondition)
		instance.Status.Conditions.Remove(corev1beta1.OpenStackControlPlaneExposeHorizonReadyCondition)
		return ctrl.Result{}, nil
	}

	// add selector to service overrides
	serviceOverrides := map[service.Endpoint]service.RoutedOverrideSpec{}
	if instance.Spec.Horizon.Template.Override.Service != nil {
		serviceOverrides[service.EndpointPublic] = *instance.Spec.Horizon.Template.Override.Service
	}

	// add selector to service overrides
	serviceOverrides[service.EndpointPublic] = AddServiceOpenStackOperatorLabel(
		serviceOverrides[service.EndpointPublic],
		horizon.Name)

	// When component services got created check if there is the need to create a route
	if err := helper.GetClient().Get(ctx, types.NamespacedName{Name: "horizon", Namespace: instance.Namespace}, horizon); err != nil {
		if !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	// preserve any previously set TLS certs, set CA cert
	if instance.Spec.TLS.PodLevel.Enabled {
		instance.Spec.Horizon.Template.TLS = horizon.Spec.TLS
	}
	instance.Spec.Horizon.Template.TLS.CaBundleSecretName = instance.Status.TLS.CaBundleSecretName

	svcs, err := service.GetServicesListWithLabel(
		ctx,
		helper,
		instance.Namespace,
		GetServiceOpenStackOperatorLabel(horizon.Name),
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// make sure to get to EndpointConfig when all service got created
	if len(svcs.Items) == 1 {
		endpointDetails, ctrlResult, err := EnsureEndpointConfig(
			ctx,
			instance,
			helper,
			horizon,
			svcs,
			serviceOverrides,
			instance.Spec.Horizon.APIOverride,
			corev1beta1.OpenStackControlPlaneExposeHorizonReadyCondition,
			false, // TODO (mschuppert) could be removed when all integrated service support TLS
			tls.API{
				API: tls.APIService{
					Public: tls.GenericService{
						SecretName: instance.Spec.Horizon.Template.TLS.SecretName,
					},
				},
			},
		)
		if err != nil {
			return ctrlResult, err
		} else if (ctrlResult != ctrl.Result{}) {
			return ctrlResult, nil
		}
		// set service overrides
		serviceOverrides = endpointDetails.GetEndpointServiceOverrides()
		// update TLS settings with cert secret
		instance.Spec.Horizon.Template.TLS.SecretName = endpointDetails.GetEndptCertSecret(service.EndpointPublic)
	}

	Log.Info("Reconcile Horizon", "horizon.Namespace", instance.Namespace, "horizon.Name", "horizon")
	op, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), horizon, func() error {
		instance.Spec.Horizon.Template.DeepCopyInto(&horizon.Spec)
		horizon.Spec.ContainerImage = *version.Status.ContainerImages.HorizonImage
		horizon.Spec.Override.Service = ptr.To(serviceOverrides[service.EndpointPublic])

		err := controllerutil.SetControllerReference(helper.GetBeforeObject(), horizon, helper.GetScheme())
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			corev1beta1.OpenStackControlPlaneHorizonReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			corev1beta1.OpenStackControlPlaneHorizonReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		Log.Info(fmt.Sprintf("Horizon %s - %s", horizon.Name, op))
	}

	if horizon.IsReady() {
		instance.Status.Conditions.MarkTrue(corev1beta1.OpenStackControlPlaneHorizonReadyCondition, corev1beta1.OpenStackControlPlaneHorizonReadyMessage)
	} else {
		instance.Status.Conditions.Set(condition.FalseCondition(
			corev1beta1.OpenStackControlPlaneHorizonReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			corev1beta1.OpenStackControlPlaneHorizonReadyRunningMessage))
	}
	instance.Status.ContainerImages.HorizonImage = version.Status.ContainerImages.HorizonImage

	return ctrl.Result{}, nil
}
