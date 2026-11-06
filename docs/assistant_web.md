# OpenStackAssistant web endpoint

Set `spec.web.enabled: true` after publishing a compatible
`openstack-assistant-ui` image and deploying an operator build with the web CRD.
The web image must implement the environment and API contract in the sibling
`openstack-assistant-ui` repository. The packaged operator supplies defaults
for both the UI and OAuth proxy images, so `web.enabled: true` is sufficient
when using that deployment. Override the UI image with
`RELATED_IMAGE_OPENSTACK_ASSISTANT_WEB_IMAGE_URL_DEFAULT` or
`spec.web.containerImage` to use a tested build. Override the OAuth proxy image
with `RELATED_IMAGE_OPENSTACK_ASSISTANT_OAUTH_PROXY_IMAGE_URL_DEFAULT` or
`spec.web.oauthProxy.image`. A standalone manager needs the UI image environment
variable or the CR field because it has no compiled UI image fallback.

```yaml
apiVersion: assistant.openstack.org/v1beta1
kind: OpenStackAssistant
metadata:
  name: example
  namespace: openstack
spec:
  containerImage: quay.io/openstack-s2i-containers/openstack-goose@sha256:REPLACE_WITH_TESTED_DIGEST
  lightspeedStack:
    baseURL: https://lightspeed-app-server.openstack-lightspeed.svc:8443/v1
    model: REPLACE_WITH_MODEL
  storage:
    pvcName: example-assistant-home
  mcpServers:
  - name: openstack
    openStackClientRef: openstackclient
  web:
    enabled: true
    containerImage: quay.io/REPLACE_WITH_WEB_IMAGE_AND_DIGEST
    port: 3000
    # route:
    #   spec:
    #     host: assistant.example.com
    # oauthProxy:
    #   cookieSecretName: example-existing-cookie
```

The controller creates an instance-selected Service on port 443, a reencrypt
Route, an ingress NetworkPolicy, a service CA ConfigMap, and private cookie and
agent Secrets. The OpenShift service CA
controller issues the Service's serving certificate. The Route trusts the
injected service CA bundle. The pod runs Goose ACP and the web API on loopback;
only the OAuth proxy's HTTPS port is exposed by the Service. The Route is not
marked ready until it is admitted and the three containers pass readiness.

The OAuth proxy requires the user to have `get` permission for this specific
`OpenStackAssistant` resource in its namespace. Grant that permission to the
intended users or groups through ordinary namespace RBAC. It replaces the
`X-Forwarded-User` header before forwarding to the API, which uses that trusted
identity to scope every conversation. The proxy's service account OAuth
redirect is registered through a Route reference annotation.

The Goose image entrypoint starts ACP in web mode. CLI and web conversations
load the same generated extensions and any administrator ConfigMap mounted at
`/etc/openstack-goose/config-overlay`. With the current Goose image, that
includes the Developer extension and its `oc` shell access unless the overlay
disables it. Only administrators trusted to configure the assistant should edit
the overlay; web users can invoke tools with the assistant ServiceAccount's
permissions. Goose's current autonomous mode does not require a web approval
for those commands.

The web database is stored in the `web` subdirectory of the assistant working
volume. With a PVC it survives pod replacement; with the default `emptyDir`
history is ephemeral. The agent and web sidecars use a private shared secret,
which the controller preserves across normal reconciles. Changing cookie,
agent, TLS, CA, or administrator configuration causes the pod to be replaced.
Disabling web removes only owned web resources and the managed OAuth redirect;
it removes a serving certificate verified as issued for its Service and
preserves user supplied Secrets, ConfigMaps and PVCs.

## Development validation

After a compatible web image and operator build are available, create a separate
assistant instance and verify:

1. The Service exposes only port 443, targets proxy port 8443, and selects the
   instance label. The Route uses reencrypt TLS and its destination CA matches
   the injected service CA bundle.
2. An authorized user can sign in, stream Goose tool activity and an answer,
   cancel a turn, and reload history.
3. A second authorized user cannot load, prompt, cancel, or delete the first
   user's conversation ID. A user without `get` permission for the assistant is
   denied at the OAuth proxy.
4. Browsers cannot reach ACP port 3284 or web API port 3000 through a Service or
   Route. Attempts to submit upstream IDs, MCP URLs or provider changes to the
   web API are rejected.
5. Pod replacement retains history with PVC storage. Changing the cookie and
   agent Secrets replaces the pod; disabling `spec.web.enabled` removes the
   Route, Service, NetworkPolicy, managed ConfigMaps and generated Secrets.

The existing namespace MCP implementation still uses shared administrative
OpenStack credentials and does not authenticate an exact assistant workload.
The web Route should not be treated as a production multi-user OpenStack tool
boundary until the namespace MCP path authenticates the exact assistant
workload and uses appropriately restricted service credentials.

## Development pod result (2026-11-05)

A temporary three-container pod in the `openstack` namespace used these image
digests: Goose `sha256:fab4bff82db8ce2e7bb7b9cfa5f25e0c3e14040a7392268946a63fb2208f9222`,
web `sha256:7b26cac6847a068d15477505b218571a488432598710f698c56037ffb2cb5d15`,
and OAuth proxy `sha256:d532b6d4b9e7c1476e4dcb7f7b60427578e65c62a906ae8e939eacdec8be4517`.
Its Service serving certificate and injected CA were issued, the reencrypt
Route was admitted, and all three containers became ready. The Route returned
403 for unauthenticated requests and a forged `X-Forwarded-User` header.

Through the private web API in that pod, a Goose prompt produced MCP tool start
and update events plus streamed text; a follow-up turn completed. The earlier
web image also supported direct Lightspeed chat, which has since been removed.
A second test identity could not list,
load, prompt, cancel or delete the first identity's conversation. A live Goose
cancel returned `stop reason: cancelled`. After replacing the pod with an
`emptyDir` working volume, its old conversation ID returned 404. The pod needed
the existing `service=openstackassistant` label to reach the namespace MCP
server's NetworkPolicy.

An interactive OAuth browser login and an operator-managed web-enabled CR still
need cluster verification with an operator build that includes this change.
