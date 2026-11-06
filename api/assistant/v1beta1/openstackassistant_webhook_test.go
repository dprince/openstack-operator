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
	"github.com/openstack-k8s-operators/lib-common/modules/common/route"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newWebTestAssistant() *OpenStackAssistant {
	return &OpenStackAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "test-assistant", Namespace: "openstack"},
		Spec: OpenStackAssistantSpec{
			ContainerImage: "example.test/goose:latest",
			LightspeedStack: LightspeedStackSpec{
				BaseURL: "https://lightspeed.example.test",
				Model:   "default",
			},
		},
	}
}

func TestValidateWeb_Disabled_NoWarnings(t *testing.T) {
	g := gomega.NewWithT(t)
	assistant := newWebTestAssistant()

	warnings, err := assistant.ValidateCreate()

	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(warnings).To(gomega.BeEmpty())
}

func TestValidateWeb_EnabledWithOAuthProxyAndMCP_NoWarnings(t *testing.T) {
	g := gomega.NewWithT(t)
	assistant := newWebTestAssistant()
	assistant.Spec.MCPServers = []MCPServerRef{{Name: "openstack", URL: "https://mcp.example.test/"}}
	assistant.Spec.Web = &WebSpec{
		Enabled:        true,
		ContainerImage: "example.test/web:latest",
		OAuthProxy:     &OAuthProxySpec{Image: "example.test/oauth-proxy:latest"},
	}
	assistant.Spec.ExtraConfig = []ConfigMount{{Name: "goose-overlay", MountPath: "/etc/openstack-goose/config-overlay"}}

	warnings, err := assistant.ValidateCreate()

	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(warnings).To(gomega.BeEmpty())
}

func TestValidateWeb_MissingOAuthProxyRejected(t *testing.T) {
	g := gomega.NewWithT(t)
	assistant := newWebTestAssistant()
	assistant.Spec.MCPServers = []MCPServerRef{{Name: "openstack", URL: "https://mcp.example.test/"}}
	assistant.Spec.Web = &WebSpec{
		Enabled:        true,
		ContainerImage: "example.test/web:latest",
	}

	warnings, err := assistant.ValidateCreate()

	g.Expect(err).To(gomega.HaveOccurred())
	g.Expect(warnings).To(gomega.BeEmpty())
}

func TestValidateWeb_NoMCPServers_Warns(t *testing.T) {
	g := gomega.NewWithT(t)
	assistant := newWebTestAssistant()
	assistant.Spec.Web = &WebSpec{
		Enabled:        true,
		ContainerImage: "example.test/web:latest",
		OAuthProxy:     &OAuthProxySpec{Image: "example.test/oauth-proxy:latest"},
	}

	warnings, err := assistant.ValidateCreate()

	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(warnings).To(gomega.ContainElement(gomega.ContainSubstring("no OpenStack tools")))
}

func TestValidateWeb_ValidateUpdate_SameWarnings(t *testing.T) {
	g := gomega.NewWithT(t)
	assistant := newWebTestAssistant()
	assistant.Spec.Web = &WebSpec{
		Enabled:        true,
		ContainerImage: "example.test/web:latest",
		OAuthProxy:     &OAuthProxySpec{Image: "example.test/oauth-proxy:latest"},
	}

	warnings, err := assistant.ValidateUpdate(nil)

	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect(warnings).NotTo(gomega.BeEmpty())
}

func TestValidateWeb_RejectsBoundaryOverrides(t *testing.T) {
	g := gomega.NewWithT(t)
	assistant := newWebTestAssistant()
	assistant.Spec.Web = &WebSpec{Enabled: true, ContainerImage: "example.test/web:1", OAuthProxy: &OAuthProxySpec{Image: "example.test/proxy:1"}}
	assistant.Spec.Web.Port = 3284
	_, err := assistant.ValidateCreate()
	g.Expect(err).To(gomega.HaveOccurred())
	assistant.Spec.Web.Port = AssistantDefaultWebPort
	assistant.Spec.Env = []corev1.EnvVar{{Name: "GOOSE_ADDITIONAL_CONFIG_FILES", Value: "/tmp/unsafe"}}
	_, err = assistant.ValidateCreate()
	g.Expect(err).To(gomega.HaveOccurred())
	assistant.Spec.Env = nil
	assistant.Spec.Web.Route = &route.OverrideSpec{Spec: &route.Spec{Path: "/other"}}
	_, err = assistant.ValidateCreate()
	g.Expect(err).To(gomega.HaveOccurred())
	assistant.Spec.Web.Route = nil
	assistant.Spec.ExtraConfig = []ConfigMount{{Name: "overlay-one", MountPath: "/etc/openstack-goose/config-overlay"}, {Name: "overlay-two", MountPath: "/etc/openstack-goose/config-overlay"}}
	_, err = assistant.ValidateCreate()
	g.Expect(err).To(gomega.HaveOccurred())
	for _, mount := range []string{"/etc/openstack-goose", "/home/goose/.config/goose/config.yaml", "/etc/pki/ca-trust", "/var/run/secrets"} {
		assistant.Spec.ExtraConfig = []ConfigMount{{Name: "unsafe", MountPath: mount}}
		_, err = assistant.ValidateCreate()
		g.Expect(err).To(gomega.HaveOccurred(), mount)
	}
}
