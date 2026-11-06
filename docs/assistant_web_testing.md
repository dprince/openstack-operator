# Test the OpenStackAssistant web endpoint

This is a development test of the web integration. Install matching Goose,
operator, and web images before creating a web-enabled assistant. Interactive
browser OAuth login and an operator-managed web CR have not yet been verified.
The temporary three-container development pod used for earlier streaming
tests was removed.

## 1. Build and roll out matching images and CRD

Build and push the Goose image with web-aware entrypoint. Publish it before
rolling out the operator change, because the older image defaults to
`keepalive` when the operator no longer passes `goose serve` arguments:

```sh
cd ../s2i-openstack-containers
STREAM=master ./build.sh build goose/goose
podman tag localhost/openstack/openstack-goose:master-latest quay.io/YOUR_ACCOUNT/openstack-goose:web-entrypoint
podman push quay.io/YOUR_ACCOUNT/openstack-goose:web-entrypoint
```

Build the combined web server/frontend image from the sibling repository and
push it to a registry your cluster can pull from:

```sh
cd ../openstack-assistant-ui
podman build -f server.Dockerfile -t quay.io/YOUR_ACCOUNT/openstack-assistant-ui:step4 .
podman push quay.io/YOUR_ACCOUNT/openstack-assistant-ui:step4
```

Build and push the operator image from this repository:

```sh
cd ../openstack-operator
make docker-build docker-push IMG=quay.io/YOUR_ACCOUNT/openstack-operator:assistant-web-step4
```

Roll out that operator image using your test cluster's existing operator
installation process, including its webhook and RBAC. The manager Deployment in
the current lab cluster is owned by the top-level `OpenStack` resource. This
tree includes the updated assistant CRD in both `config/crd/bases/` and
`bindata/crds/crds.yaml`. Confirm the rollout and schema before proceeding:

```sh
oc -n openstack-operators get deployment openstack-operator-controller-manager -o json \
  | jq -r '.spec.template.spec.containers[] | select(.name == "manager") | .image'
oc get crd openstackassistants.assistant.openstack.org -o json \
  | jq -r '.spec.versions[] | select(.name == "v1beta1") | .schema.openAPIV3Schema.properties.spec.properties | has("web")'
```

The second command must print `true`. If your test-cluster rollout does not
update the CRD, apply [the generated assistant
CRD](../config/crd/bases/assistant.openstack.org_openstackassistants.yaml)
before creating the test CR:

```sh
oc apply --server-side -f config/crd/bases/assistant.openstack.org_openstackassistants.yaml
```

The earlier pod test used
`quay.io/dprince/openstack-assistant-ui@sha256:7b26cac6847a068d15477505b218571a488432598710f698c56037ffb2cb5d15`.
That image predates the Goose-only API and expects direct Lightspeed settings
that the operator no longer supplies. Build a new web image from the sibling
repository and make it pullable by the generated assistant ServiceAccount.

## 2. Create a separate test assistant

Save the following as `/tmp/assistant-web-test.yaml`, replacing the web image
with your pullable image. The example uses the model and namespace MCP service
configured in the lab cluster on 2026-11-05. It uses `emptyDir` storage, so chat
history disappears when the pod is replaced.

```yaml
apiVersion: assistant.openstack.org/v1beta1
kind: OpenStackAssistant
metadata:
  name: assistant-web-test
  namespace: openstack
spec:
  containerImage: quay.io/YOUR_ACCOUNT/openstack-goose:web-entrypoint
  caBundleSecretName: combined-ca-bundle
  lightspeedStack:
    baseURL: https://lightspeed-app-server.openstack-lightspeed.svc:8443/v1
    model: provider-gpt-5-6-terra/gpt-5-6-terra
  mcpServers:
  - name: openstack
    openStackClientRef: openstackclient
  web:
    enabled: true
    containerImage: quay.io/YOUR_ACCOUNT/openstack-assistant-ui:step4
    port: 3000
    oauthProxy:
      image: quay.io/openshift/origin-oauth-proxy@sha256:d532b6d4b9e7c1476e4dcb7f7b60427578e65c62a906ae8e939eacdec8be4517
```

```sh
oc apply -f /tmp/assistant-web-test.yaml
```

Use a separate CR to avoid changing the existing assistant and its persistent
history during development. The existing assistant's ConfigMap overlay is now
supported in web mode and can be tested separately.

## 3. Authorize a test user and open the Route

The OAuth proxy requires `get` permission for this specific
`OpenStackAssistant`. If your browser user does not already have it, grant a
namespaced Role and RoleBinding. Replace `YOUR_OCP_USERNAME` with that user's
OpenShift identity:

```sh
oc -n openstack create role assistant-web-test-read --verb=get \
  --resource=openstackassistants.assistant.openstack.org \
  --resource-name=assistant-web-test
oc -n openstack create rolebinding assistant-web-test-read \
  --role=assistant-web-test-read --user=YOUR_OCP_USERNAME
```

Add `--user=SECOND_OCP_USERNAME` to the RoleBinding command if you want to
check isolation between two authorized users.

This follows [OpenShift RBAC](https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/authentication_and_authorization/using-rbac)
and the [OAuth proxy SAR configuration](https://github.com/openshift/oauth-proxy/blob/master/README.md).

Wait for the assistant's actual condition name, `OpenStackAssistantReady`, then
get the Route URL:

```sh
oc -n openstack wait --for=condition=OpenStackAssistantReady \
  openstackassistant/assistant-web-test --timeout=10m
oc -n openstack get pod assistant-web-test
oc -n openstack get openstackassistant assistant-web-test \
  -o jsonpath='{.status.webURL}{"\n"}'
```

The pod should show `3/3` ready. Open `status.webURL` in a browser and sign in
as the authorized user. Try an OpenStack/Goose question such as “Which compute
services are down?” and confirm tool progress and streamed text. Send a
follow-up, cancel a turn, and reload the page to confirm history is retained.
With a second authorized user, check that the first user's
conversations are absent. A user without `get` permission should be denied.

If the pod reports `ImagePullBackOff`, make the web image pullable by the cluster
or link an existing pull Secret to ServiceAccount
`openstackassistant-assistant-web-test`, then replace the failed test pod.

## 4. Clean up

```sh
oc -n openstack delete openstackassistant assistant-web-test
oc -n openstack delete rolebinding/assistant-web-test-read \
  role/assistant-web-test-read --ignore-not-found
```

The controller removes its owned Route, Service, NetworkPolicy, generated
credentials and verified service-ca certificate. See [web endpoint
configuration](assistant_web.md) for the full resource and security contract.
