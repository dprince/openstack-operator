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
	"fmt"
	"path"
	"reflect"
	"strings"

	"github.com/openstack-k8s-operators/lib-common/modules/common/route"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// ValidateCreate implements webhook.Validator
func (r *OpenStackAssistant) ValidateCreate() (admission.Warnings, error) {
	return r.validateWeb()
}

// ValidateUpdate implements webhook.Validator
func (r *OpenStackAssistant) ValidateUpdate(_ runtime.Object) (admission.Warnings, error) {
	return r.validateWeb()
}

func (r *OpenStackAssistant) validateWeb() (admission.Warnings, error) {
	if !r.WebEnabled() {
		return nil, nil
	}
	w := r.Spec.Web
	if w.ContainerImage == "" || w.OAuthProxy == nil || w.OAuthProxy.Image == "" {
		return nil, fmt.Errorf("web and OAuth proxy container images are required")
	}
	if r.WebPort() < 1024 || r.WebPort() > 65535 || r.WebPort() == 3284 || r.WebPort() == 8443 {
		return nil, fmt.Errorf("web port must be between 1024 and 65535 and differ from agent and proxy ports")
	}
	if w.Route != nil && w.Route.Spec != nil {
		allowed := *w.Route.Spec.DeepCopy()
		allowed.Host = ""
		if !reflect.DeepEqual(allowed, route.Spec{}) {
			return nil, fmt.Errorf("web route override may set only host")
		}
	}
	for _, e := range r.Spec.Env {
		name := strings.ToUpper(e.Name)
		if name == "HOME" || name == "SSL_CERT_FILE" || name == "REQUESTS_CA_BUNDLE" || name == "NODE_EXTRA_CA_CERTS" || strings.HasPrefix(name, "ASSISTANT_") || strings.HasPrefix(name, "GOOSE_") || strings.HasPrefix(name, "OPENSTACK_GOOSE_") || strings.HasPrefix(name, "LIGHTSPEED_") || strings.HasPrefix(name, "MCP_SERVER_") {
			return nil, fmt.Errorf("environment variable %s is reserved when web is enabled", e.Name)
		}
	}
	managedMounts := []string{
		"/etc/assistant-private",
		"/tmp/openstack-goose",
		"/var/run/secrets/kubernetes.io",
		tls.DownstreamTLSCABundlePath,
		r.WorkingDir(),
	}
	const overlayPath = "/etc/openstack-goose/config-overlay"
	overlaySeen := false
	for _, cfg := range r.Spec.ExtraConfig {
		if mountPathsOverlap(cfg.MountPath, overlayPath) {
			if cfg.MountPath != overlayPath || overlaySeen {
				return nil, fmt.Errorf("extraConfig mountPath %s overlaps the Goose config overlay", cfg.MountPath)
			}
			overlaySeen = true
		}
		for _, managed := range managedMounts {
			if !mountPathsOverlap(cfg.MountPath, managed) {
				continue
			}
			return nil, fmt.Errorf("extraConfig mountPath %s conflicts with a managed mount", cfg.MountPath)
		}
	}
	if len(r.Spec.MCPServers) == 0 {
		return admission.Warnings{"web mode has no OpenStack tools configured"}, nil
	}
	return nil, nil
}

func mountPathsOverlap(a, b string) bool {
	a, b = path.Clean(a), path.Clean(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// ValidateDelete implements webhook.Validator
func (r *OpenStackAssistant) ValidateDelete() (admission.Warnings, error) {
	return nil, nil
}
