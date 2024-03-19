package openstack

import (
	"context"
	"fmt"

	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	corev1beta1 "github.com/openstack-k8s-operators/openstack-operator/apis/core/v1beta1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

// ReconcileDesignate -
func ReconcileDesignate(ctx context.Context, instance *corev1beta1.OpenStackControlPlane, version *corev1beta1.OpenStackVersion, helper *helper.Helper) (ctrl.Result, error) {
	designate := &designatev1.Designate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "designate",
			Namespace: instance.Namespace,
		},
	}

	if !instance.Spec.Designate.Enabled {
		if res, err := EnsureDeleted(ctx, helper, designate); err != nil {
			return res, err
		}
		instance.Status.Conditions.Remove(corev1beta1.OpenStackControlPlaneDesignateReadyCondition)
		instance.Status.Conditions.Remove(corev1beta1.OpenStackControlPlaneExposeDesignateReadyCondition)
		return ctrl.Result{}, nil
	}

	// add selector to service overrides
	for _, endpointType := range []service.Endpoint{service.EndpointPublic, service.EndpointInternal} {
		if instance.Spec.Designate.Template.DesignateAPI.Override.Service == nil {
			instance.Spec.Designate.Template.DesignateAPI.Override.Service = map[service.Endpoint]service.RoutedOverrideSpec{}
		}
		instance.Spec.Designate.Template.DesignateAPI.Override.Service[endpointType] =
			AddServiceOpenStackOperatorLabel(
				instance.Spec.Designate.Template.DesignateAPI.Override.Service[endpointType],
				designate.Name)
	}

	// When component services got created check if there is the need to create a route
	if err := helper.GetClient().Get(ctx, types.NamespacedName{Name: "designate", Namespace: instance.Namespace}, designate); err != nil {
		if !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	svcs, err := service.GetServicesListWithLabel(
		ctx,
		helper,
		instance.Namespace,
		GetServiceOpenStackOperatorLabel(designate.Name),
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// make sure to get to EndpointConfig when all service got created
	if len(svcs.Items) == len(instance.Spec.Designate.Template.DesignateAPI.Override.Service) {
		endpointDetails, ctrlResult, err := EnsureEndpointConfig(
			ctx,
			instance,
			helper,
			designate,
			svcs,
			instance.Spec.Designate.Template.DesignateAPI.Override.Service,
			instance.Spec.Designate.APIOverride,
			corev1beta1.OpenStackControlPlaneExposeDesignateReadyCondition,
			true, // TODO: (mschuppert) disable TLS for now until implemented
			tls.API{},
		)
		if err != nil {
			return ctrlResult, err
		} else if (ctrlResult != ctrl.Result{}) {
			return ctrlResult, nil
		}
		// set service overrides
		instance.Spec.Designate.Template.DesignateAPI.Override.Service = endpointDetails.GetEndpointServiceOverrides()
	}

	helper.GetLogger().Info("Reconciling Designate", "Designate.Namespace", instance.Namespace, "Designate.Name", "designate")
	op, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), designate, func() error {
		// FIXME: the designate structs need some rework (images should be at the top level, not in the sub structs)
		instance.Spec.Designate.Template.DesignateSpecBase.DeepCopyInto(&designate.Spec.DesignateSpecBase)
		// API
		instance.Spec.Designate.Template.DesignateAPI.DesignateAPISpecBase.DeepCopyInto(&designate.Spec.DesignateAPI.DesignateAPISpecBase)
		instance.Spec.Designate.Template.DesignateAPI.DesignateServiceTemplateCore.DeepCopyInto(&designate.Spec.DesignateAPI.DesignateServiceTemplateCore)
		// Central
		instance.Spec.Designate.Template.DesignateCentral.DesignateCentralSpecBase.DeepCopyInto(&designate.Spec.DesignateCentral.DesignateCentralSpecBase)
		instance.Spec.Designate.Template.DesignateCentral.DesignateServiceTemplateCore.DeepCopyInto(&designate.Spec.DesignateCentral.DesignateServiceTemplateCore)
		// Worker
		instance.Spec.Designate.Template.DesignateWorker.DesignateWorkerSpecBase.DeepCopyInto(&designate.Spec.DesignateWorker.DesignateWorkerSpecBase)
		instance.Spec.Designate.Template.DesignateWorker.DesignateServiceTemplateCore.DeepCopyInto(&designate.Spec.DesignateWorker.DesignateServiceTemplateCore)
		// Mdns
		instance.Spec.Designate.Template.DesignateMdns.DesignateMdnsSpecBase.DeepCopyInto(&designate.Spec.DesignateMdns.DesignateMdnsSpecBase)
		instance.Spec.Designate.Template.DesignateMdns.DesignateServiceTemplateCore.DeepCopyInto(&designate.Spec.DesignateMdns.DesignateServiceTemplateCore)
		// Producer
		instance.Spec.Designate.Template.DesignateProducer.DesignateProducerSpecBase.DeepCopyInto(&designate.Spec.DesignateProducer.DesignateProducerSpecBase)
		instance.Spec.Designate.Template.DesignateProducer.DesignateServiceTemplateCore.DeepCopyInto(&designate.Spec.DesignateProducer.DesignateServiceTemplateCore)
		// Bind9
		instance.Spec.Designate.Template.DesignateBackendbind9.DesignateBackendbind9SpecBase.DeepCopyInto(&designate.Spec.DesignateBackendbind9.DesignateBackendbind9SpecBase)
		instance.Spec.Designate.Template.DesignateBackendbind9.DesignateServiceTemplateCore.DeepCopyInto(&designate.Spec.DesignateBackendbind9.DesignateServiceTemplateCore)

		designate.Spec.DesignateAPI.ContainerImage = *version.Status.ContainerImages.DesignateApiImage
		designate.Spec.DesignateCentral.ContainerImage = *version.Status.ContainerImages.DesignateCentralImage
		designate.Spec.DesignateMdns.ContainerImage = *version.Status.ContainerImages.DesignateMdnsImage
		designate.Spec.DesignateProducer.ContainerImage = *version.Status.ContainerImages.DesignateProducerImage
		designate.Spec.DesignateWorker.ContainerImage = *version.Status.ContainerImages.DesignateWorkerImage
		designate.Spec.DesignateBackendbind9.ContainerImage = *version.Status.ContainerImages.DesignateBackendbind9Image
		designate.Spec.DesignateUnbound.ContainerImage = *version.Status.ContainerImages.DesignateUnboundImage

		if designate.Spec.Secret == "" {
			designate.Spec.Secret = instance.Spec.Secret
		}
		if designate.Spec.NodeSelector == nil && instance.Spec.NodeSelector != nil {
			designate.Spec.NodeSelector = instance.Spec.NodeSelector
		}
		if designate.Spec.DatabaseInstance == "" {
			//designate.Spec.DatabaseInstance = instance.Name // name of MariaDB we create here
			designate.Spec.DatabaseInstance = "openstack" //FIXME: see above
		}
		err := controllerutil.SetControllerReference(helper.GetBeforeObject(), designate, helper.GetScheme())
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			corev1beta1.OpenStackControlPlaneDesignateReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			corev1beta1.OpenStackControlPlaneDesignateReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		helper.GetLogger().Info(fmt.Sprintf("Designate %s - %s", designate.Name, op))
	}

	if designate.IsReady() {
		instance.Status.Conditions.MarkTrue(corev1beta1.OpenStackControlPlaneDesignateReadyCondition, corev1beta1.OpenStackControlPlaneDesignateReadyMessage)
	} else {
		instance.Status.Conditions.Set(condition.FalseCondition(
			corev1beta1.OpenStackControlPlaneDesignateReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			corev1beta1.OpenStackControlPlaneDesignateReadyRunningMessage))
	}

	instance.Status.ContainerImages.DesignateApiImage = version.Status.ContainerImages.DesignateApiImage
	instance.Status.ContainerImages.DesignateCentralImage = version.Status.ContainerImages.DesignateCentralImage
	instance.Status.ContainerImages.DesignateMdnsImage = version.Status.ContainerImages.DesignateMdnsImage
	instance.Status.ContainerImages.DesignateProducerImage = version.Status.ContainerImages.DesignateProducerImage
	instance.Status.ContainerImages.DesignateWorkerImage = version.Status.ContainerImages.DesignateWorkerImage
	instance.Status.ContainerImages.DesignateBackendbind9Image = version.Status.ContainerImages.DesignateBackendbind9Image
	instance.Status.ContainerImages.DesignateUnboundImage = version.Status.ContainerImages.DesignateUnboundImage

	return ctrl.Result{}, nil

}
