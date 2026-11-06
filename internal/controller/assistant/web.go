package assistant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/route"
	assistantv1 "github.com/openstack-k8s-operators/openstack-operator/api/assistant/v1beta1"
	"github.com/openstack-k8s-operators/openstack-operator/internal/openstackassistant"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	webServiceCAAnnotation = "service.beta.openshift.io/serving-cert-secret-name"
	webInjectCAAnnotation  = "service.beta.openshift.io/inject-cabundle"
	webCertServiceName     = "service.beta.openshift.io/originating-service-name"
	webCertServiceUID      = "service.beta.openshift.io/originating-service-uid"
	webRedirectAnnotation  = "serviceaccounts.openshift.io/oauth-redirectreference.web"
	webInstanceLabel       = "assistant.openstack.org/instance-id"
)

type webRuntime struct {
	Origin           string
	TLSSecretName    string
	CookieSecretName string
	AgentSecretName  string
	WebPort          int32
}

func (w *webRuntime) toPodConfig() *openstackassistant.WebPodConfig {
	if w == nil {
		return nil
	}
	return &openstackassistant.WebPodConfig{Origin: w.Origin, TLSSecretName: w.TLSSecretName, CookieSecretName: w.CookieSecretName, AgentSecretName: w.AgentSecretName, Port: w.WebPort}
}

func boundedWebName(instance *assistantv1.OpenStackAssistant, suffix string) string {
	name := instance.Name + suffix
	if len(name) <= 63 {
		return name
	}
	sum := sha256.Sum256([]byte(instance.Namespace + "/" + name))
	return name[:46] + "-" + hex.EncodeToString(sum[:8])
}
func webResourceName(instance *assistantv1.OpenStackAssistant, suffix string) string {
	return boundedWebName(instance, "-web"+suffix)
}
func webName(instance *assistantv1.OpenStackAssistant) string { return webResourceName(instance, "") }
func webAgentConfigName(instance *assistantv1.OpenStackAssistant) string {
	return webResourceName(instance, "-agent-config")
}
func webInstanceID(instance *assistantv1.OpenStackAssistant) string {
	sum := sha256.Sum256([]byte(instance.Namespace + "/" + instance.Name + "/" + string(instance.UID)))
	return hex.EncodeToString(sum[:8])
}
func hashBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func waitWeb() ctrl.Result          { return ctrl.Result{RequeueAfter: 10 * time.Second} }

func (r *OpenStackAssistantReconciler) webOwned(ctx context.Context, instance *assistantv1.OpenStackAssistant, object client.Object) (bool, error) {
	err := r.Get(ctx, client.ObjectKeyFromObject(object), object)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !metav1.IsControlledBy(object, instance) {
		return true, fmt.Errorf("web resource %T %s already exists without assistant ownership", object, object.GetName())
	}
	return true, nil
}

func (r *OpenStackAssistantReconciler) webDeleteOwned(ctx context.Context, instance *assistantv1.OpenStackAssistant, object client.Object) error {
	err := r.Get(ctx, client.ObjectKeyFromObject(object), object)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !metav1.IsControlledBy(object, instance) {
		return nil
	}
	return r.Delete(ctx, object)
}

func (r *OpenStackAssistantReconciler) webServiceUID(ctx context.Context, instance *assistantv1.OpenStackAssistant) (string, error) {
	service := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: webName(instance)}, service)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !metav1.IsControlledBy(service, instance) {
		return "", nil
	}
	return string(service.UID), nil
}

func (r *OpenStackAssistantReconciler) webDeleteServiceCATLS(ctx context.Context, instance *assistantv1.OpenStackAssistant, serviceUID string) error {
	if serviceUID == "" {
		return nil
	}
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: webResourceName(instance, "-tls")}, secret)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if secret.Annotations[webCertServiceName] != webName(instance) || secret.Annotations[webCertServiceUID] != serviceUID {
		return nil
	}
	return r.Delete(ctx, secret)
}

func (r *OpenStackAssistantReconciler) webSecret(ctx context.Context, instance *assistantv1.OpenStackAssistant, name, key string) ([]byte, error) {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}}
	exists, err := r.webOwned(ctx, instance, secret)
	if err != nil {
		return nil, err
	}
	if !exists {
		bytes := make([]byte, 32)
		if _, err = rand.Read(bytes); err != nil {
			return nil, err
		}
		value := []byte(hex.EncodeToString(bytes))
		if key == "session_secret" {
			value = []byte(base64.StdEncoding.EncodeToString(bytes))
		}
		secret.Data = map[string][]byte{key: value}
		secret.Type = corev1.SecretTypeOpaque
		if err = controllerutil.SetControllerReference(instance, secret, r.Scheme); err != nil {
			return nil, err
		}
		if err = r.Create(ctx, secret); err != nil {
			return nil, err
		}
	}
	if len(secret.Data[key]) == 0 {
		return nil, fmt.Errorf("web Secret %s missing %s", name, key)
	}
	return secret.Data[key], nil
}

func (r *OpenStackAssistantReconciler) webCookieSecret(ctx context.Context, instance *assistantv1.OpenStackAssistant, name string) ([]byte, error) {
	if instance.Spec.Web.OAuthProxy.CookieSecretName == "" {
		return r.webSecret(ctx, instance, name, "session_secret")
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: name}, secret); err != nil {
		return nil, err
	}
	if len(secret.Data["session_secret"]) == 0 {
		return nil, fmt.Errorf("cookie Secret %s missing session_secret", name)
	}
	return secret.Data["session_secret"], nil
}

func (r *OpenStackAssistantReconciler) webServiceAccountAnnotation(ctx context.Context, instance *assistantv1.OpenStackAssistant, enabled bool) error {
	sa := &corev1.ServiceAccount{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: instance.RbacResourceName()}, sa); err != nil {
		if !enabled && apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	value := fmt.Sprintf(`{"kind":"OAuthRedirectReference","apiVersion":"v1","reference":{"kind":"Route","name":%q}}`, webName(instance))
	if enabled {
		if sa.Annotations == nil {
			sa.Annotations = map[string]string{}
		}
		if sa.Annotations[webRedirectAnnotation] == value {
			return nil
		}
		if _, exists := sa.Annotations[webRedirectAnnotation]; exists {
			return fmt.Errorf("service account %s already has an OAuth web redirect reference", sa.Name)
		}
		sa.Annotations[webRedirectAnnotation] = value
	} else {
		if sa.Annotations[webRedirectAnnotation] != value {
			return nil
		}
		delete(sa.Annotations, webRedirectAnnotation)
	}
	return r.Update(ctx, sa)
}

func (r *OpenStackAssistantReconciler) cleanupWeb(ctx context.Context, instance *assistantv1.OpenStackAssistant) error {
	name := webName(instance)
	serviceUID, err := r.webServiceUID(ctx, instance)
	if err != nil {
		return err
	}
	for _, obj := range []client.Object{
		&routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: webResourceName(instance, "-ca"), Namespace: instance.Namespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: webAgentConfigName(instance), Namespace: instance.Namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: webResourceName(instance, "-cookie"), Namespace: instance.Namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: webResourceName(instance, "-agent"), Namespace: instance.Namespace}},
	} {
		if err := r.webDeleteOwned(ctx, instance, obj); err != nil {
			return err
		}
	}
	if err := r.webDeleteServiceCATLS(ctx, instance, serviceUID); err != nil {
		return err
	}
	return r.webServiceAccountAnnotation(ctx, instance, false)
}

// reconcileWeb creates only authenticated web resources and returns a runtime
// contract once the Route, service certificate, and CA are available.
func (r *OpenStackAssistantReconciler) reconcileWeb(ctx context.Context, instance *assistantv1.OpenStackAssistant, _ *helper.Helper, labels map[string]string, configVars map[string]env.Setter) (*webRuntime, ctrl.Result, error) {
	name := webName(instance)
	if !instance.WebEnabled() {
		instance.Status.WebURL = ""
		return nil, ctrl.Result{}, r.cleanupWeb(ctx, instance)
	}
	instance.Status.WebURL = ""
	if instance.Spec.Web.ContainerImage == "" || instance.Spec.Web.OAuthProxy == nil || instance.Spec.Web.OAuthProxy.Image == "" {
		return nil, ctrl.Result{}, fmt.Errorf("web and OAuth proxy images are required")
	}
	selector := map[string]string{webInstanceLabel: webInstanceID(instance)}
	for key, value := range labels {
		selector[key] = value
	}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}}
	if _, err := r.webOwned(ctx, instance, svc); err != nil {
		return nil, ctrl.Result{}, err
	}
	_, err := controllerutil.CreateOrPatch(ctx, r.Client, svc, func() error {
		svc.Labels = selector
		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		svc.Annotations[webServiceCAAnnotation] = webResourceName(instance, "-tls")
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.Selector = selector
		svc.Spec.Ports = []corev1.ServicePort{{Name: "https", Port: 443, TargetPort: intstr.FromInt32(8443), Protocol: corev1.ProtocolTCP}}
		return controllerutil.SetControllerReference(instance, svc, r.Scheme)
	})
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	np := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}}
	if _, err := r.webOwned(ctx, instance, np); err != nil {
		return nil, ctrl.Result{}, err
	}
	_, err = controllerutil.CreateOrPatch(ctx, r.Client, np, func() error {
		np.Spec.PodSelector = metav1.LabelSelector{MatchLabels: selector}
		np.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
		np.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"network.openshift.io/policy-group": "ingress"}}}}, Ports: []networkingv1.NetworkPolicyPort{{Protocol: ptrTCP(), Port: ptrPort(8443)}}}}
		return controllerutil.SetControllerReference(instance, np, r.Scheme)
	})
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	ca := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: webResourceName(instance, "-ca"), Namespace: instance.Namespace}}
	if _, err := r.webOwned(ctx, instance, ca); err != nil {
		return nil, ctrl.Result{}, err
	}
	_, err = controllerutil.CreateOrPatch(ctx, r.Client, ca, func() error {
		if ca.Annotations == nil {
			ca.Annotations = map[string]string{}
		}
		ca.Annotations[webInjectCAAnnotation] = "true"
		return controllerutil.SetControllerReference(instance, ca, r.Scheme)
	})
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	// Older web pods used an operator-owned Goose override. Remove it after
	// moving extension configuration back to the image and administrator overlay.
	if err := r.webDeleteOwned(ctx, instance, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: webAgentConfigName(instance), Namespace: instance.Namespace}}); err != nil {
		return nil, ctrl.Result{}, err
	}
	if err := r.webServiceAccountAnnotation(ctx, instance, true); err != nil {
		return nil, ctrl.Result{}, err
	}
	cookieName := webResourceName(instance, "-cookie")
	if instance.Spec.Web.OAuthProxy.CookieSecretName != "" {
		cookieName = instance.Spec.Web.OAuthProxy.CookieSecretName
	}
	cookie, err := r.webCookieSecret(ctx, instance, cookieName)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	agent, err := r.webSecret(ctx, instance, webResourceName(instance, "-agent"), "secret")
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	configVars["web-cookie"] = env.SetValue(hashBytes(cookie))
	configVars["web-agent"] = env.SetValue(hashBytes(agent))
	tlsSecret := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: webResourceName(instance, "-tls")}, tlsSecret)
	if apierrors.IsNotFound(err) {
		return nil, waitWeb(), nil
	}
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	if svc.UID == "" || tlsSecret.Annotations[webCertServiceName] != name || tlsSecret.Annotations[webCertServiceUID] != string(svc.UID) {
		return nil, ctrl.Result{}, fmt.Errorf("web serving certificate Secret %s was not issued for Service %s", tlsSecret.Name, name)
	}
	if len(tlsSecret.Data["tls.crt"]) == 0 || len(tlsSecret.Data["tls.key"]) == 0 || ca.Data["service-ca.crt"] == "" {
		return nil, waitWeb(), nil
	}
	configVars["web-tls"] = env.SetValue(hashBytes(append(append([]byte{}, tlsSecret.Data["tls.crt"]...), tlsSecret.Data["tls.key"]...)))
	configVars["web-ca"] = env.SetValue(hashBytes([]byte(ca.Data["service-ca.crt"])))
	desired := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace, Labels: selector, Annotations: map[string]string{"haproxy.router.openshift.io/hsts_header": "max-age=31536000;includeSubDomains"}}, Spec: routev1.RouteSpec{
		To:   routev1.RouteTargetReference{Kind: "Service", Name: name},
		Port: &routev1.RoutePort{TargetPort: intstr.FromString("https")},
		TLS:  &routev1.TLSConfig{Termination: routev1.TLSTerminationReencrypt, InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect, DestinationCACertificate: ca.Data["service-ca.crt"]},
	}}
	if instance.Spec.Web.Route != nil {
		wrapped, err := route.NewRoute(desired, 10*time.Second, []route.OverrideSpec{*instance.Spec.Web.Route})
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		desired = wrapped.GetRoute()
		// The transport and backend are always managed, even if a stale CR
		// bypassed admission validation.
		desired.Spec.To = routev1.RouteTargetReference{Kind: "Service", Name: name}
		desired.Spec.Port = &routev1.RoutePort{TargetPort: intstr.FromString("https")}
		desired.Spec.TLS = &routev1.TLSConfig{Termination: routev1.TLSTerminationReencrypt, InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect, DestinationCACertificate: ca.Data["service-ca.crt"]}
		desired.Annotations["haproxy.router.openshift.io/hsts_header"] = "max-age=31536000;includeSubDomains"
	}
	actual := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}}
	if _, err := r.webOwned(ctx, instance, actual); err != nil {
		return nil, ctrl.Result{}, err
	}
	_, err = controllerutil.CreateOrPatch(ctx, r.Client, actual, func() error {
		actual.Labels = desired.Labels
		actual.Annotations = desired.Annotations
		if desired.Spec.Host == "" && actual.Spec.Host != "" {
			desired.Spec.Host = actual.Spec.Host
		}
		actual.Spec = desired.Spec
		return controllerutil.SetControllerReference(instance, actual, r.Scheme)
	})
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	for _, ingress := range actual.Status.Ingress {
		for _, condition := range ingress.Conditions {
			if condition.Type == routev1.RouteAdmitted && condition.Status == corev1.ConditionTrue && ingress.Host != "" && (actual.Spec.Host == "" || actual.Spec.Host == ingress.Host) {
				return &webRuntime{Origin: "https://" + ingress.Host, TLSSecretName: webResourceName(instance, "-tls"), CookieSecretName: cookieName, AgentSecretName: webResourceName(instance, "-agent"), WebPort: instance.WebPort()}, ctrl.Result{}, nil
			}
		}
	}
	return nil, waitWeb(), nil
}

func ptrTCP() *corev1.Protocol               { value := corev1.ProtocolTCP; return &value }
func ptrPort(port int32) *intstr.IntOrString { value := intstr.FromInt32(port); return &value }
