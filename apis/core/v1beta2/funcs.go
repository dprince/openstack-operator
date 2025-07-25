/*
Copyright 2023 Red Hat, Inc.

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

package v1beta2

import (
	"context"

	keystonev2 "github.com/openstack-k8s-operators/keystone-operator/api/v1beta2"
	corev1beta1 "github.com/openstack-k8s-operators/openstack-operator/apis/core/v1beta1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// KeystoneDefault is a no-op to be compatible with the old API
// This function can be removed once openstack-operator is not calling the
// Default method on the keystone spec anymore.
func KeystoneDefault(spec *keystonev2.KeystoneAPISpecCore) {
	// nothing to do
}

// KeystoneSetDefaultRouteAnnotations is a no-op to be compatible with the old API
// This function can be removed once openstack-operator is not calling the
// SetDefaultRouteAnnotations method on the keystone spec anymore.
func KeystoneSetDefaultRouteAnnotations(spec *keystonev2.KeystoneAPISpecCore) {
	// nothing to do
}

// KeystoneValidateCreate validates if KeystoneAPI fields are compatible
func KeystoneValidateCreate(spec *keystonev2.KeystoneAPISpecCore, path *field.Path, namespace string) field.ErrorList {
	return field.ErrorList{}
}

// KeystoneValidateUpdate is a no-op to be compatible with the old API
// This function can be removed once openstack-operator is not calling the
// ValidateUpdate method on the keystone spec anymore.
func KeystoneValidateUpdate(spec *keystonev2.KeystoneAPISpecCore, old keystonev2.KeystoneAPISpecCore, path *field.Path, namespace string) field.ErrorList {
	//TODO: This is a no-op function to be compatible with the old API.
	// This function can be removed once openstack-operator is not calling the
	// ValidateUpdate method on the keystone spec anymore.
	return field.ErrorList{}
}

// GetOpenStackVersions - returns the OpenStackVersion resource(s) associated with the namespace
// This function calls the v1beta1 version since OpenStackVersion only exists in v1beta1
func GetOpenStackVersions(namespace string, k8sClient client.Client) (*corev1beta1.OpenStackVersionList, error) {
	versionList := &corev1beta1.OpenStackVersionList{}

	listOpts := []client.ListOption{
		client.InNamespace(namespace),
	}

	if err := k8sClient.List(context.TODO(), versionList, listOpts...); err != nil {
		return nil, err
	}

	return versionList, nil
}
