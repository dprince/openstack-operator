package functional_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	assistantv1 "github.com/openstack-k8s-operators/openstack-operator/api/assistant/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("OpenStackAssistant web admission", func() {
	newAssistant := func(name string) *assistantv1.OpenStackAssistant {
		return &assistantv1.OpenStackAssistant{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: assistantv1.OpenStackAssistantSpec{
				ContainerImage:  "example.test/agent:1",
				LightspeedStack: assistantv1.LightspeedStackSpec{BaseURL: "https://lightspeed.example.test/v1", Model: "model"},
				Web:             &assistantv1.WebSpec{Enabled: true, ContainerImage: "example.test/web:1", OAuthProxy: &assistantv1.OAuthProxySpec{Image: "example.test/oauth-proxy:1"}},
			},
		}
	}

	It("defaults the private port and admits an authenticated web spec", func() {
		cr := newAssistant("assistant-web-valid")
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, cr)).To(Succeed()) })
		Expect(cr.Spec.Web.Port).To(Equal(assistantv1.AssistantDefaultWebPort))
	})

	It("defaults both web images when only web is enabled", func() {
		webImage := os.Getenv("RELATED_IMAGE_OPENSTACK_ASSISTANT_WEB_IMAGE_URL_DEFAULT")
		proxyImage := os.Getenv("RELATED_IMAGE_OPENSTACK_ASSISTANT_OAUTH_PROXY_IMAGE_URL_DEFAULT")
		if webImage == "" || proxyImage == "" {
			Skip("web image defaults are not configured for this test run")
		}
		cr := newAssistant("assistant-web-image-defaults")
		cr.Spec.Web = &assistantv1.WebSpec{Enabled: true}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, cr)).To(Succeed()) })
		Expect(cr.Spec.Web.ContainerImage).To(Equal(webImage))
		Expect(cr.Spec.Web.OAuthProxy).NotTo(BeNil())
		Expect(cr.Spec.Web.OAuthProxy.Image).To(Equal(proxyImage))
	})

	It("admits the administrator Goose overlay in web mode", func() {
		cr := newAssistant("assistant-web-overlay")
		cr.Spec.ExtraConfig = []assistantv1.ConfigMount{{Name: "goose-config", MountPath: "/etc/openstack-goose/config-overlay"}}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, cr)).To(Succeed()) })
	})

	It("rejects a conflicting listener port", func() {
		cr := newAssistant("assistant-web-port-conflict")
		cr.Spec.Web.Port = 3284
		Expect(k8sClient.Create(ctx, cr)).NotTo(Succeed())
	})

	It("rejects a mount that overlaps the Goose overlay path", func() {
		cr := newAssistant("assistant-web-overlay-overlap")
		cr.Spec.ExtraConfig = []assistantv1.ConfigMount{{Name: "goose-config", MountPath: "/etc/openstack-goose"}}
		Expect(k8sClient.Create(ctx, cr)).NotTo(Succeed())
	})
})
