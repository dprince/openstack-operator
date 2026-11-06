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

// Package openstackassistant provides functionality for managing OpenStack assistant resources
package openstackassistant

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	env "github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/pod"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"github.com/openstack-k8s-operators/lib-common/modules/users"
	assistantv1 "github.com/openstack-k8s-operators/openstack-operator/api/assistant/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// workingDirVolumeName is the name of the writable working-directory volume.
const workingDirVolumeName = "working-dir"

// WebPodConfig contains only reconciled, administrator-controlled web inputs.
type WebPodConfig struct {
	Origin           string
	TLSSecretName    string
	CookieSecretName string
	AgentSecretName  string
	Port             int32
}

// ValidateMCPServerRefs validates MCP server names and literal URLs before
// they are exported to the assistant pod.
func ValidateMCPServerRefs(mcpServers []assistantv1.MCPServerRef) error {
	seen := make(map[string]string, len(mcpServers))
	for _, mcp := range mcpServers {
		canonicalName := strings.ToLower(mcp.Name)
		if previousName, exists := seen[canonicalName]; exists {
			return fmt.Errorf("MCP server name %q conflicts with %q when compared case-insensitively", mcp.Name, previousName)
		}
		seen[canonicalName] = mcp.Name
		if err := ValidateMCPServerURL(mcp.Name, mcp.URL); err != nil {
			return err
		}
	}
	return nil
}

// ValidateMCPServerURL rejects values that cannot safely be passed through an
// environment variable to the assistant container.
func ValidateMCPServerURL(name string, url string) error {
	for _, char := range url {
		if unicode.IsControl(char) {
			return fmt.Errorf("MCP server %q URL contains control character %U", name, char)
		}
	}
	return nil
}

// ValidateCustomMCPServerURLs validates literal MCP server URLs supplied
// through the custom pod environment. ValueFrom entries are validated by the
// entrypoint after Kubernetes resolves them.
func ValidateCustomMCPServerURLs(envVars []corev1.EnvVar) error {
	for _, envVar := range envVars {
		if strings.HasPrefix(envVar.Name, "MCP_SERVER_") && envVar.ValueFrom == nil {
			if err := ValidateMCPServerURL(envVar.Name, envVar.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// AssistantPodSpec returns the PodSpec for the assistant pod.
// resolvedMCPServers maps MCP server name to URL for all MCP servers
// (both manually specified and auto-resolved from OpenStackClientRef).
//
// The operator is agent-agnostic: it injects a generic set of environment
// variables and mounts the CA bundle plus any ExtraConfig ConfigMaps. The
// image's own entrypoint is responsible for rendering the harness-specific
// configuration from these inputs (and for deriving the LLM API key from the
// pod service-account token).
func AssistantPodSpec(
	instance *assistantv1.OpenStackAssistant,
	configHash string,
	resolvedMCPServers map[string]string,
) corev1.PodSpec {
	return AssistantPodSpecWithCABundle(
		instance,
		configHash,
		resolvedMCPServers,
		instance.Spec.CaBundleSecretName,
	)
}

// AssistantPodSpecWithCABundle returns the assistant PodSpec using the
// effective CA bundle Secret reconciled by the controller. Keeping the source
// Secret from the API separate from the mounted Secret lets the controller add
// cluster-provided trust roots without modifying user-owned data.
func AssistantPodSpecWithCABundle(
	instance *assistantv1.OpenStackAssistant,
	configHash string,
	resolvedMCPServers map[string]string,
	caBundleSecretName string,
) corev1.PodSpec {
	return AssistantPodSpecWithWeb(instance, configHash, resolvedMCPServers, caBundleSecretName, nil)
}

func AssistantPodSpecWithWeb(
	instance *assistantv1.OpenStackAssistant,
	configHash string,
	resolvedMCPServers map[string]string,
	caBundleSecretName string,
	web *WebPodConfig,
) corev1.PodSpec {
	envVars := map[string]env.Setter{}
	envVars["CONFIG_HASH"] = env.SetValue(configHash)
	envVars["LIGHTSPEED_URL"] = env.SetValue(instance.Spec.LightspeedStack.BaseURL)
	envVars["LIGHTSPEED_MODEL"] = env.SetValue(instance.Spec.LightspeedStack.Model)
	envVars["HOME"] = env.SetValue(instance.WorkingDir())

	// Additional models are passed as a JSON array for the image entrypoint to
	// render into the harness config.
	if len(instance.Spec.LightspeedStack.AdditionalModels) > 0 {
		if encoded, err := json.Marshal(instance.Spec.LightspeedStack.AdditionalModels); err == nil {
			envVars["LIGHTSPEED_ADDITIONAL_MODELS"] = env.SetValue(string(encoded))
		}
	}

	if caBundleSecretName != "" {
		envVars["SSL_CERT_FILE"] = env.SetValue(tls.DownstreamTLSCABundlePath)
	}

	for name, url := range resolvedMCPServers {
		envVars["MCP_SERVER_"+name] = env.SetValue(url)
	}

	containerEnvs := env.MergeEnvs([]corev1.EnvVar{}, envVars)
	for idx := range instance.Spec.Env {
		e := instance.Spec.Env[idx]
		containerEnvs = env.MergeEnvs(containerEnvs, env.SetterMap{
			e.Name: func(env *corev1.EnvVar) {
				env.Value = e.Value
				env.ValueFrom = e.ValueFrom
			},
		})
	}

	volumes := assistantPodVolumes(instance, caBundleSecretName)
	volumeMounts := assistantPodVolumeMounts(instance, caBundleSecretName)

	podSpec := corev1.PodSpec{
		TerminationGracePeriodSeconds: ptr.To[int64](0),
		ServiceAccountName:            instance.RbacResourceName(),
		SecurityContext:               pod.RestrictivePodSecurityContext(users.OpenStackAssistantUID, users.OpenStackAssistantGID),
		Volumes:                       volumes,
		Containers: []corev1.Container{
			{
				Name:            "assistant",
				Image:           instance.Spec.ContainerImage,
				SecurityContext: pod.RestrictiveSecurityContext(users.OpenStackAssistantUID, users.OpenStackAssistantGID),
				Env:             containerEnvs,
				VolumeMounts:    volumeMounts,
				Resources:       instance.Spec.Resources,
			},
		},
	}

	if instance.Spec.NodeSelector != nil {
		podSpec.NodeSelector = *instance.Spec.NodeSelector
	}
	if web != nil {
		addWebToPod(instance, &podSpec, web)
	}

	return podSpec
}

func addWebToPod(instance *assistantv1.OpenStackAssistant, spec *corev1.PodSpec, web *WebPodConfig) {
	spec.TerminationGracePeriodSeconds = ptr.To[int64](30)
	agent := &spec.Containers[0]
	agent.Env = append(agent.Env,
		corev1.EnvVar{Name: "ASSISTANT_WEB_ENABLED", Value: "true"},
		corev1.EnvVar{Name: "ASSISTANT_AGENT_SECRET_FILE", Value: "/etc/assistant-private/agent/secret"},
	)
	agent.VolumeMounts = append(agent.VolumeMounts,
		corev1.VolumeMount{Name: "agent-secret", MountPath: "/etc/assistant-private/agent", ReadOnly: true},
	)
	agent.ReadinessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/3284"}}}, InitialDelaySeconds: 3, PeriodSeconds: 10}
	webEnv := []corev1.EnvVar{
		{Name: "ASSISTANT_UID", Value: string(instance.UID)},
		{Name: "ASSISTANT_NAMESPACE", Value: instance.Namespace},
		{Name: "ASSISTANT_NAME", Value: instance.Name},
		{Name: "ASSISTANT_PUBLIC_ORIGIN", Value: web.Origin},
		{Name: "ASSISTANT_AGENT_URL", Value: "ws://127.0.0.1:3284/acp"},
		{Name: "ASSISTANT_AGENT_SECRET_FILE", Value: "/etc/assistant-private/agent/secret"},
		{Name: "ASSISTANT_AGENT_CWD", Value: instance.WorkingDir()},
		{Name: "ASSISTANT_WEB_DB_PATH", Value: "/var/lib/assistant-web/conversations.sqlite"},
		{Name: "ASSISTANT_WEB_PORT", Value: strconv.Itoa(int(web.Port))},
		{Name: "ASSISTANT_WEB_HOST", Value: "127.0.0.1"},
	}
	webMounts := []corev1.VolumeMount{
		{Name: workingDirVolumeName, MountPath: "/var/lib/assistant-web", SubPath: "web"},
		{Name: "agent-secret", MountPath: "/etc/assistant-private/agent", ReadOnly: true},
	}
	webPort := strconv.Itoa(int(web.Port))
	webContainer := corev1.Container{
		Name: "web", Image: instance.Spec.Web.ContainerImage,
		SecurityContext: pod.RestrictiveSecurityContext(users.OpenStackAssistantUID, users.OpenStackAssistantGID),
		Env:             webEnv, VolumeMounts: webMounts, Resources: instance.Spec.Web.Resources,
		Ports:          []corev1.ContainerPort{{Name: "web-private", ContainerPort: web.Port, Protocol: corev1.ProtocolTCP}},
		ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"node", "-e", fmt.Sprintf("let s=require('net').connect(%s,'127.0.0.1',()=>{s.end();process.exit(0)});s.on('error',()=>process.exit(1))", webPort)}}}, InitialDelaySeconds: 3, PeriodSeconds: 10},
	}
	sar := fmt.Sprintf(`{"namespace":%q,"group":"assistant.openstack.org","resource":"openstackassistants","resourceName":%q,"verb":"get"}`, instance.Namespace, instance.Name)
	proxy := corev1.Container{
		Name: "oauth-proxy", Image: instance.Spec.Web.OAuthProxy.Image,
		SecurityContext: pod.RestrictiveSecurityContext(users.OpenStackAssistantUID, users.OpenStackAssistantGID),
		Resources:       instance.Spec.Web.OAuthProxy.Resources,
		Args: []string{
			"--provider=openshift", "--http-address=", "--https-address=:8443",
			"--openshift-service-account=" + instance.RbacResourceName(),
			"--openshift-sar=" + sar,
			"--upstream=http://127.0.0.1:" + webPort,
			"--tls-cert=/etc/assistant-private/tls/tls.crt", "--tls-key=/etc/assistant-private/tls/tls.key",
			"--cookie-secret-file=/etc/assistant-private/cookie/session_secret",
			"--pass-user-headers=true", "--pass-host-header=true", "--cookie-secure=true", "--cookie-httponly=true", "--cookie-samesite=lax",
			"--upstream-timeout=1h", "--upstream-flush=5ms",
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "web-tls", MountPath: "/etc/assistant-private/tls", ReadOnly: true},
			{Name: "web-cookie", MountPath: "/etc/assistant-private/cookie", ReadOnly: true},
		},
		Ports:          []corev1.ContainerPort{{Name: "oauth-https", ContainerPort: 8443, Protocol: corev1.ProtocolTCP}},
		ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/oauth/healthz", Port: intstr.FromInt32(8443), Scheme: corev1.URISchemeHTTPS}}, InitialDelaySeconds: 3, PeriodSeconds: 10},
	}
	spec.InitContainers = append(spec.InitContainers, corev1.Container{
		Name: "web-storage", Image: instance.Spec.ContainerImage,
		Command:         []string{"/bin/sh", "-c", "mkdir -p /work/web && chmod 0770 /work/web"},
		SecurityContext: pod.RestrictiveSecurityContext(users.OpenStackAssistantUID, users.OpenStackAssistantGID),
		VolumeMounts:    []corev1.VolumeMount{{Name: workingDirVolumeName, MountPath: "/work"}},
	})
	spec.Containers = append(spec.Containers, webContainer, proxy)
	spec.Volumes = append(spec.Volumes,
		corev1.Volume{Name: "agent-secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: web.AgentSecretName}}},
		corev1.Volume{Name: "web-cookie", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: web.CookieSecretName}}},
		corev1.Volume{Name: "web-tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: web.TLSSecretName}}},
	)
}

// extraConfigVolumeName returns a deterministic, RFC1123-valid volume name for
// the ExtraConfig entry at the given index. The index guarantees uniqueness
// regardless of the ConfigMap name characters.
func extraConfigVolumeName(idx int) string {
	return fmt.Sprintf("extra-config-%d", idx)
}

func assistantPodVolumeMounts(
	instance *assistantv1.OpenStackAssistant,
	caBundleSecretName string,
) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{}

	// Writable working directory (also HOME) for the agent's configuration,
	// sessions, and runtime state, since all other mounts are read-only.
	mounts = append(mounts, corev1.VolumeMount{
		Name:      workingDirVolumeName,
		MountPath: instance.WorkingDir(),
	})

	for idx, cfg := range instance.Spec.ExtraConfig {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      extraConfigVolumeName(idx),
			MountPath: cfg.MountPath,
			ReadOnly:  true,
		})
	}

	ca := tls.Ca{CaBundleSecretName: caBundleSecretName}
	mounts = append(mounts, ca.CreateVolumeMounts(nil)...)

	return mounts
}

func assistantPodVolumes(
	instance *assistantv1.OpenStackAssistant,
	caBundleSecretName string,
) []corev1.Volume {
	volumes := []corev1.Volume{}

	// Writable working directory: an existing PVC when requested, otherwise an
	// ephemeral emptyDir.
	workingDirVolume := corev1.Volume{Name: workingDirVolumeName}
	if instance.Spec.Storage != nil && instance.Spec.Storage.PVCName != "" {
		workingDirVolume.VolumeSource = corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: instance.Spec.Storage.PVCName,
			},
		}
	} else {
		workingDirVolume.VolumeSource = corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		}
	}
	volumes = append(volumes, workingDirVolume)

	for idx, cfg := range instance.Spec.ExtraConfig {
		volumes = append(volumes, corev1.Volume{
			Name: extraConfigVolumeName(idx),
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cfg.Name,
					},
				},
			},
		})
	}

	if caBundleSecretName != "" {
		ca := tls.Ca{CaBundleSecretName: caBundleSecretName}
		volumes = append(volumes, ca.CreateVolume())
	}

	return volumes
}
