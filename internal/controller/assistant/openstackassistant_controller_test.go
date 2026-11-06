/*
Copyright 2026.

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

package assistant

import (
	"context"
	"strings"
	"testing"

	. "github.com/onsi/gomega" //revive:disable:dot-imports

	routev1 "github.com/openshift/api/route/v1"
	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	assistantv1 "github.com/openstack-k8s-operators/openstack-operator/api/assistant/v1beta1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	fakekube "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func init() {
	_ = routev1.AddToScheme(scheme.Scheme)
	_ = assistantv1.AddToScheme(scheme.Scheme)
}

// setupWebTestReconciler creates a fake-client-backed reconciler and a
// lib-common helper bound to instance, mirroring the pattern used in
// internal/openstack/common_test.go for controller-logic unit tests that
// don't need a full envtest environment.
func setupWebTestReconciler(instance *assistantv1.OpenStackAssistant, objects ...client.Object) (*OpenStackAssistantReconciler, *helper.Helper) {
	s := scheme.Scheme
	hasSA := false
	for _, object := range objects {
		if object.GetName() == instance.RbacResourceName() {
			hasSA = true
		}
	}
	if !hasSA {
		objects = append(objects, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: instance.RbacResourceName(), Namespace: instance.Namespace}})
	}

	allObjects := append([]client.Object{instance}, objects...)
	fakeClient := fakeclient.NewClientBuilder().
		WithScheme(s).
		WithObjects(allObjects...).
		WithStatusSubresource(&routev1.Route{}).
		Build()

	fakeKubeClient := fakekube.NewSimpleClientset()

	r := &OpenStackAssistantReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	h, err := helper.NewHelper(instance, fakeClient, fakeKubeClient, s, ctrl.Log.WithName("test"))
	if err != nil {
		panic(err)
	}
	return r, h
}

func newWebReconcileTestInstance() *assistantv1.OpenStackAssistant {
	return &assistantv1.OpenStackAssistant{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-assistant",
			Namespace: "openstack",
			UID:       "assistant-uid",
		},
		Spec: assistantv1.OpenStackAssistantSpec{
			ContainerImage: "example.test/goose:latest",
			LightspeedStack: assistantv1.LightspeedStackSpec{
				BaseURL: "https://lightspeed.example.test",
				Model:   "default",
			},
			Web: &assistantv1.WebSpec{
				Enabled:        true,
				Port:           assistantv1.AssistantDefaultWebPort,
				ContainerImage: "example.test/web:latest",
				OAuthProxy: &assistantv1.OAuthProxySpec{
					Image: "example.test/oauth-proxy:latest",
				},
			},
		},
	}
}

var assistantWebLabels = map[string]string{common.AppSelector: "openstackassistant"}

func TestWebResourceNamesAreBounded(t *testing.T) {
	g := NewWithT(t)
	instance := newWebReconcileTestInstance()
	instance.Name = strings.Repeat("a", 63)
	for _, suffix := range []string{"", "-tls", "-ca", "-cookie", "-agent", "-agent-config"} {
		name := webResourceName(instance, suffix)
		g.Expect(name).To(HaveLen(63))
		g.Expect(name).To(MatchRegexp(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`))
	}
	g.Expect(webResourceName(instance, "-tls")).NotTo(Equal(webResourceName(instance, "-ca")))
	firstInstanceID := webInstanceID(instance)
	instance.UID = types.UID("replacement")
	g.Expect(webInstanceID(instance)).NotTo(Equal(firstInstanceID))
}

func TestReconcileWeb_Disabled_NoOp(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	instance := newWebReconcileTestInstance()
	instance.Spec.Web.Enabled = false
	instance.Status.WebURL = "https://stale.example.test"
	r, h := setupWebTestReconciler(instance)

	configVars := map[string]env.Setter{}
	webConfig, result, err := r.reconcileWeb(ctx, instance, h, assistantWebLabels, configVars)

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))
	g.Expect(webConfig).To(BeNil())
	g.Expect(instance.Status.WebURL).To(BeEmpty())
}

func TestReconcileWeb_Disabled_CleansUpExistingResources(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	instance := newWebReconcileTestInstance()
	instance.Spec.Web.Enabled = false

	webName := instance.Name + "-web"
	existingSvc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: webName, Namespace: instance.Namespace}}
	existingRoute := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: webName, Namespace: instance.Namespace}}
	existingNetPol := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: webName, Namespace: instance.Namespace}}
	existingCookie := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: webName + "-cookie", Namespace: instance.Namespace}}
	userCookie := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "user-cookie", Namespace: instance.Namespace}, Data: map[string][]byte{"session_secret": []byte("user value")}}
	serviceTLS := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: webName + "-tls", Namespace: instance.Namespace}}
	for _, obj := range []client.Object{existingSvc, existingRoute, existingNetPol, existingCookie} {
		g.Expect(controllerutil.SetControllerReference(instance, obj, scheme.Scheme)).To(Succeed())
	}

	r, h := setupWebTestReconciler(instance, existingSvc, existingRoute, existingNetPol, existingCookie, userCookie, serviceTLS)

	_, _, err := r.reconcileWeb(ctx, instance, h, assistantWebLabels, map[string]env.Setter{})
	g.Expect(err).NotTo(HaveOccurred())

	for _, obj := range []client.Object{existingSvc, existingRoute, existingNetPol, existingCookie} {
		err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		g.Expect(err).To(HaveOccurred(), "expected %T %s to be deleted", obj, obj.GetName())
	}
	g.Expect(r.Get(ctx, client.ObjectKeyFromObject(userCookie), userCookie)).To(Succeed())
	g.Expect(r.Get(ctx, client.ObjectKeyFromObject(serviceTLS), serviceTLS)).To(Succeed())
}

func TestReconcileWeb_Disabled_DeletesOnlyServiceCACertificate(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	instance := newWebReconcileTestInstance()
	instance.Spec.Web.Enabled = false
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: webName(instance), Namespace: instance.Namespace, UID: "service-uid"}}
	g.Expect(controllerutil.SetControllerReference(instance, service, scheme.Scheme)).To(Succeed())
	certificate := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: webResourceName(instance, "-tls"), Namespace: instance.Namespace,
		Annotations: map[string]string{webCertServiceName: service.Name, webCertServiceUID: string(service.UID)},
	}}
	r, h := setupWebTestReconciler(instance, service, certificate)
	_, _, err := r.reconcileWeb(ctx, instance, h, assistantWebLabels, map[string]env.Setter{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(r.Get(ctx, client.ObjectKeyFromObject(certificate), certificate)).ToNot(Succeed())
}

func TestReconcileWeb_EnabledWithOAuthProxy_WaitsForServiceCATLSSecret(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	instance := newWebReconcileTestInstance()
	oldAgentConfig := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: webAgentConfigName(instance), Namespace: instance.Namespace}}
	g.Expect(controllerutil.SetControllerReference(instance, oldAgentConfig, scheme.Scheme)).To(Succeed())
	r, h := setupWebTestReconciler(instance, oldAgentConfig)

	configVars := map[string]env.Setter{}
	webConfig, result, err := r.reconcileWeb(ctx, instance, h, assistantWebLabels, configVars)

	g.Expect(err).NotTo(HaveOccurred())
	// No service-ca operator runs in a unit test, so the serving-certificate
	// Secret never appears: the controller must wait rather than proceed.
	g.Expect(result.RequeueAfter).To(BeNumerically(">", 0))
	g.Expect(webConfig).To(BeNil())
	g.Expect(r.Get(ctx, client.ObjectKeyFromObject(oldAgentConfig), oldAgentConfig)).ToNot(Succeed())

	webName := instance.Name + "-web"
	svc := &corev1.Service{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: webName, Namespace: instance.Namespace}, svc)).To(Succeed())
	g.Expect(svc.Annotations).To(HaveKeyWithValue(
		"service.beta.openshift.io/serving-cert-secret-name", webName+"-tls"))
	g.Expect(svc.Spec.Ports).To(HaveLen(1))
	g.Expect(svc.Spec.Ports[0].Port).To(Equal(int32(443)))
	g.Expect(svc.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(8443)))
	g.Expect(svc.Spec.Selector).To(HaveKeyWithValue(webInstanceLabel, webInstanceID(instance)))
	g.Expect(svc.Spec.Selector).To(HaveKeyWithValue(common.AppSelector, "openstackassistant"))

	netPol := &networkingv1.NetworkPolicy{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: webName, Namespace: instance.Namespace}, netPol)).To(Succeed())
	g.Expect(netPol.Spec.PodSelector.MatchLabels).To(HaveKeyWithValue(webInstanceLabel, webInstanceID(instance)))

	// The Route must not be created before the TLS secret exists, since
	// oauth-proxy cannot start without it.
	route := &routev1.Route{}
	err = r.Get(ctx, types.NamespacedName{Name: webName, Namespace: instance.Namespace}, route)
	g.Expect(err).To(HaveOccurred())
}

func TestReconcileWeb_EnabledWithOAuthProxy_TLSSecretPresent(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	instance := newWebReconcileTestInstance()
	webName := instance.Name + "-web"
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: webName, Namespace: instance.Namespace, UID: "service-uid"}}
	g.Expect(controllerutil.SetControllerReference(instance, service, scheme.Scheme)).To(Succeed())

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: webName + "-tls", Namespace: instance.Namespace, Annotations: map[string]string{webCertServiceName: webName, webCertServiceUID: "service-uid"}},
		Data: map[string][]byte{
			"tls.crt": []byte("cert"),
			"tls.key": []byte("key"),
		},
	}
	ca := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: webName + "-ca", Namespace: instance.Namespace}, Data: map[string]string{"service-ca.crt": "CA"}}
	g.Expect(controllerutil.SetControllerReference(instance, ca, scheme.Scheme)).To(Succeed())
	r, h := setupWebTestReconciler(instance, service, tlsSecret, ca)

	configVars := map[string]env.Setter{}
	webConfig, result, err := r.reconcileWeb(ctx, instance, h, assistantWebLabels, configVars)

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(result.RequeueAfter).To(BeNumerically(">", 0))
	g.Expect(webConfig).To(BeNil())

	cookieSecret := &corev1.Secret{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: webName + "-cookie", Namespace: instance.Namespace}, cookieSecret)).To(Succeed())
	g.Expect(cookieSecret.Data).To(HaveKey("session_secret"))
	g.Expect(cookieSecret.Data["session_secret"]).NotTo(BeEmpty())
	firstCookie := append([]byte(nil), cookieSecret.Data["session_secret"]...)
	_, _, err = r.reconcileWeb(ctx, instance, h, assistantWebLabels, map[string]env.Setter{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(r.Get(ctx, client.ObjectKeyFromObject(cookieSecret), cookieSecret)).To(Succeed())
	g.Expect(cookieSecret.Data["session_secret"]).To(Equal(firstCookie))
	firstHash, err := util.HashOfInputHashes(configVars)
	g.Expect(err).NotTo(HaveOccurred())
	cookieSecret.Data["session_secret"] = []byte("rotated-cookie-secret")
	g.Expect(r.Update(ctx, cookieSecret)).To(Succeed())
	rotatedVars := map[string]env.Setter{}
	_, _, err = r.reconcileWeb(ctx, instance, h, assistantWebLabels, rotatedVars)
	g.Expect(err).NotTo(HaveOccurred())
	rotatedHash, err := util.HashOfInputHashes(rotatedVars)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(rotatedHash).NotTo(Equal(firstHash))

	route := &routev1.Route{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: webName, Namespace: instance.Namespace}, route)).To(Succeed())
	g.Expect(route.Spec.TLS).NotTo(BeNil())
	g.Expect(route.Spec.TLS.Termination).To(Equal(routev1.TLSTerminationReencrypt))
	g.Expect(route.Spec.TLS.DestinationCACertificate).To(Equal("CA"))
	g.Expect(route.Spec.To.Name).To(Equal(webName))
}

func TestReconcileWeb_RouteAdmitted_SetsWebURLAndAnnotatesServiceAccount(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	instance := newWebReconcileTestInstance()
	webName := instance.Name + "-web"
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: webName, Namespace: instance.Namespace, UID: "service-uid"}}
	g.Expect(controllerutil.SetControllerReference(instance, service, scheme.Scheme)).To(Succeed())

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: webName + "-tls", Namespace: instance.Namespace, Annotations: map[string]string{webCertServiceName: webName, webCertServiceUID: "service-uid"}},
		Data: map[string][]byte{
			"tls.crt": []byte("cert"),
			"tls.key": []byte("key"),
		},
	}
	ca := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: webName + "-ca", Namespace: instance.Namespace}, Data: map[string]string{"service-ca.crt": "CA"}}
	g.Expect(controllerutil.SetControllerReference(instance, ca, scheme.Scheme)).To(Succeed())
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: instance.RbacResourceName(), Namespace: instance.Namespace},
	}
	r, h := setupWebTestReconciler(instance, service, tlsSecret, sa, ca)

	// First pass creates the Route; no router runs in a unit test, so it
	// starts unadmitted (empty host).
	_, _, err := r.reconcileWeb(ctx, instance, h, assistantWebLabels, map[string]env.Setter{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(instance.Status.WebURL).To(BeEmpty())

	// Simulate the OpenShift router admitting the Route.
	route := &routev1.Route{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: webName, Namespace: instance.Namespace}, route)).To(Succeed())
	route.Status.Ingress = []routev1.RouteIngress{{Host: "test-assistant-web.apps.example.test", Conditions: []routev1.RouteIngressCondition{{Type: routev1.RouteAdmitted, Status: corev1.ConditionTrue}}}}
	g.Expect(r.Status().Update(ctx, route)).To(Succeed())

	webConfig, _, err := r.reconcileWeb(ctx, instance, h, assistantWebLabels, map[string]env.Setter{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(webConfig.Origin).To(Equal("https://test-assistant-web.apps.example.test"))
	g.Expect(instance.Status.WebURL).To(BeEmpty())

	g.Expect(r.Get(ctx, client.ObjectKeyFromObject(sa), sa)).To(Succeed())
	g.Expect(sa.Annotations).To(HaveKey("serviceaccounts.openshift.io/oauth-redirectreference.web"))
}

func TestReconcileWeb_RejectsForeignServingCertificate(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	instance := newWebReconcileTestInstance()
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: webName(instance), Namespace: instance.Namespace, UID: "service-uid"}}
	g.Expect(controllerutil.SetControllerReference(instance, service, scheme.Scheme)).To(Succeed())
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: webResourceName(instance, "-tls"), Namespace: instance.Namespace,
		Annotations: map[string]string{webCertServiceName: service.Name, webCertServiceUID: "other-uid"},
	}, Data: map[string][]byte{"tls.crt": []byte("cert"), "tls.key": []byte("key")}}
	r, h := setupWebTestReconciler(instance, service, secret)
	_, _, err := r.reconcileWeb(ctx, instance, h, assistantWebLabels, map[string]env.Setter{})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("not issued for Service"))
}

func TestReconcileWeb_MissingOAuthProxyRejected(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	instance := newWebReconcileTestInstance()
	instance.Spec.Web.OAuthProxy = nil
	r, h := setupWebTestReconciler(instance)

	webConfig, result, err := r.reconcileWeb(ctx, instance, h, assistantWebLabels, map[string]env.Setter{})

	g.Expect(err).To(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))
	g.Expect(webConfig).To(BeNil())
	svc := &corev1.Service{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: instance.Name + "-web", Namespace: instance.Namespace}, svc)).ToNot(Succeed())
}

func TestWebServiceAccountAnnotationPreservesExistingReference(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	instance := newWebReconcileTestInstance()
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name: instance.RbacResourceName(), Namespace: instance.Namespace,
		Annotations: map[string]string{webRedirectAnnotation: `{"kind":"OAuthRedirectReference","reference":{"kind":"Route","name":"user-route"}}`},
	}}
	r, _ := setupWebTestReconciler(instance, sa)
	g.Expect(r.webServiceAccountAnnotation(ctx, instance, true)).ToNot(Succeed())
	g.Expect(r.Get(ctx, client.ObjectKeyFromObject(sa), sa)).To(Succeed())
	g.Expect(sa.Annotations).To(HaveKeyWithValue(webRedirectAnnotation, `{"kind":"OAuthRedirectReference","reference":{"kind":"Route","name":"user-route"}}`))
}
