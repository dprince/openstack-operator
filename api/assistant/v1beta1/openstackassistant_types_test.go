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

package v1beta1

import (
	"testing"

	"github.com/onsi/gomega"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
)

func TestOpenStackAssistantDefaultCABundle(t *testing.T) {
	g := gomega.NewWithT(t)
	assistant := &OpenStackAssistant{}

	assistant.Default()

	// Default() populates an empty CaBundleSecretName with the standard
	// combined CA bundle secret so pods trust cluster-issued certs by default.
	g.Expect(assistant.Spec.CaBundleSecretName).To(gomega.Equal(tls.CABundleSecret))
}

func TestOpenStackAssistantDefaultCABundlePreserved(t *testing.T) {
	g := gomega.NewWithT(t)
	assistant := &OpenStackAssistant{}
	assistant.Spec.CaBundleSecretName = "custom-ca"

	assistant.Default()

	// An explicitly set CaBundleSecretName must not be overwritten.
	g.Expect(assistant.Spec.CaBundleSecretName).To(gomega.Equal("custom-ca"))
}

func TestOpenStackAssistantWebImageDefaults(t *testing.T) {
	g := gomega.NewWithT(t)
	previousDefaults := openStackAssistantDefaults
	defer SetupOpenStackAssistantDefaults(previousDefaults)
	t.Setenv("RELATED_IMAGE_OPENSTACK_ASSISTANT_WEB_IMAGE_URL_DEFAULT", "example.test/web:default")
	t.Setenv("RELATED_IMAGE_OPENSTACK_ASSISTANT_OAUTH_PROXY_IMAGE_URL_DEFAULT", "example.test/oauth-proxy:default")
	SetupDefaults()

	assistant := &OpenStackAssistant{Spec: OpenStackAssistantSpec{Web: &WebSpec{Enabled: true}}}
	assistant.Default()
	g.Expect(assistant.Spec.Web.Port).To(gomega.Equal(AssistantDefaultWebPort))
	g.Expect(assistant.Spec.Web.ContainerImage).To(gomega.Equal("example.test/web:default"))
	g.Expect(assistant.Spec.Web.OAuthProxy).NotTo(gomega.BeNil())
	g.Expect(assistant.Spec.Web.OAuthProxy.Image).To(gomega.Equal("example.test/oauth-proxy:default"))

	custom := &OpenStackAssistant{Spec: OpenStackAssistantSpec{Web: &WebSpec{
		Enabled:        true,
		ContainerImage: "example.test/web:custom",
		OAuthProxy:     &OAuthProxySpec{Image: "example.test/oauth-proxy:custom"},
	}}}
	custom.Default()
	g.Expect(custom.Spec.Web.ContainerImage).To(gomega.Equal("example.test/web:custom"))
	g.Expect(custom.Spec.Web.OAuthProxy.Image).To(gomega.Equal("example.test/oauth-proxy:custom"))
}
