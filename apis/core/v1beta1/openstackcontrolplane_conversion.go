/*
Copyright 2025.

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
	"encoding/json"
	"fmt"
	"reflect"

	keystonev1 "github.com/openstack-k8s-operators/keystone-operator/api/v1beta1"
	keystonev2 "github.com/openstack-k8s-operators/keystone-operator/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// log is for logging in this package.
var openstackcontrolplaneconversionlog = logf.Log.WithName("openstackcontrolplane-conversion")

// ConvertTo converts this OpenStackControlPlane to the Hub version (v1beta2).
func (src *OpenStackControlPlane) ConvertTo(dstRaw conversion.Hub) error {
	openstackcontrolplaneconversionlog.Info("Converting OpenStackControlPlane from v1beta1 to v1beta2", "name", src.Name)

	// Use JSON marshaling for conversion to avoid import cycles
	srcBytes, err := json.Marshal(src)
	if err != nil {
		return fmt.Errorf("failed to marshal v1beta1 object: %w", err)
	}

	// Unmarshal into v1beta2 structure
	err = json.Unmarshal(srcBytes, dstRaw)
	if err != nil {
		return fmt.Errorf("failed to unmarshal into v1beta2 object: %w", err)
	}

	// Handle Keystone-specific conversions that need special attention
	if err := src.convertKeystoneTemplateSpecifics(dstRaw); err != nil {
		return fmt.Errorf("failed to convert Keystone template: %w", err)
	}

	openstackcontrolplaneconversionlog.V(1).Info("Successfully converted OpenStackControlPlane from v1beta1 to v1beta2")
	return nil
}

// ConvertFrom converts from the Hub version (v1beta2) to this version.
func (dst *OpenStackControlPlane) ConvertFrom(srcRaw conversion.Hub) error {
	openstackcontrolplaneconversionlog.Info("Converting OpenStackControlPlane from v1beta2 to v1beta1")

	// Use JSON marshaling for conversion to avoid import cycles
	srcBytes, err := json.Marshal(srcRaw)
	if err != nil {
		return fmt.Errorf("failed to marshal v1beta2 object: %w", err)
	}

	// Unmarshal into v1beta1 structure
	err = json.Unmarshal(srcBytes, dst)
	if err != nil {
		return fmt.Errorf("failed to unmarshal into v1beta1 object: %w", err)
	}

	// Handle Keystone-specific conversions that need special attention
	if err := dst.convertKeystoneTemplateSpecificsFrom(srcRaw); err != nil {
		return fmt.Errorf("failed to convert Keystone template from v1beta2: %w", err)
	}

	openstackcontrolplaneconversionlog.V(1).Info("Successfully converted OpenStackControlPlane from v1beta2 to v1beta1")
	return nil
}

// convertKeystoneTemplateSpecifics handles the keystone-specific conversions from v1beta1 to v1beta2
func (src *OpenStackControlPlane) convertKeystoneTemplateSpecifics(dstRaw conversion.Hub) error {
	// If Keystone template is not specified, nothing to convert
	if src.Spec.Keystone.Template == nil {
		return nil
	}

	openstackcontrolplaneconversionlog.V(1).Info("Converting Keystone template from v1beta1 to v1beta2")

	// Use reflection to access the v1beta2 keystone template field
	dstValue := reflect.ValueOf(dstRaw).Elem()
	specField := dstValue.FieldByName("Spec")
	if !specField.IsValid() {
		return fmt.Errorf("destination object has no Spec field")
	}

	keystoneField := specField.FieldByName("Keystone")
	if !keystoneField.IsValid() {
		return fmt.Errorf("destination spec has no Keystone field")
	}

	templateField := keystoneField.FieldByName("Template")
	if !templateField.IsValid() {
		return fmt.Errorf("destination keystone has no Template field")
	}

	// Convert the Keystone v1beta1 template to v1beta2 template
	// Since both APIs are compatible in most cases, we can use JSON marshaling
	v1beta1TemplateBytes, err := json.Marshal(src.Spec.Keystone.Template)
	if err != nil {
		return fmt.Errorf("failed to marshal v1beta1 keystone template: %w", err)
	}

	v1beta2Template := keystonev2.KeystoneAPISpecCore{}
	err = json.Unmarshal(v1beta1TemplateBytes, &v1beta2Template)
	if err != nil {
		return fmt.Errorf("failed to unmarshal into v1beta2 keystone template: %w", err)
	}

	// Set the converted template
	templateField.Set(reflect.ValueOf(&v1beta2Template))

	openstackcontrolplaneconversionlog.V(1).Info("Successfully converted Keystone template from v1beta1 to v1beta2")
	return nil
}

// convertKeystoneTemplateSpecificsFrom handles the keystone-specific conversions from v1beta2 to v1beta1
func (dst *OpenStackControlPlane) convertKeystoneTemplateSpecificsFrom(srcRaw conversion.Hub) error {
	openstackcontrolplaneconversionlog.V(1).Info("Converting Keystone template from v1beta2 to v1beta1")

	// Use reflection to access the v1beta2 keystone template field
	srcValue := reflect.ValueOf(srcRaw).Elem()
	specField := srcValue.FieldByName("Spec")
	if !specField.IsValid() {
		return fmt.Errorf("source object has no Spec field")
	}

	keystoneField := specField.FieldByName("Keystone")
	if !keystoneField.IsValid() {
		return fmt.Errorf("source spec has no Keystone field")
	}

	templateField := keystoneField.FieldByName("Template")
	if !templateField.IsValid() {
		return fmt.Errorf("source keystone has no Template field")
	}

	// If template is nil, nothing to convert
	if templateField.IsNil() {
		return nil
	}

	// Convert the Keystone v1beta2 template to v1beta1 template
	// Since both APIs are compatible in most cases, we can use JSON marshaling
	v1beta2TemplateBytes, err := json.Marshal(templateField.Interface())
	if err != nil {
		return fmt.Errorf("failed to marshal v1beta2 keystone template: %w", err)
	}

	var v1beta1Template keystonev1.KeystoneAPISpecCore
	err = json.Unmarshal(v1beta2TemplateBytes, &v1beta1Template)
	if err != nil {
		return fmt.Errorf("failed to unmarshal into v1beta1 keystone template: %w", err)
	}

	dst.Spec.Keystone.Template = &v1beta1Template

	openstackcontrolplaneconversionlog.V(1).Info("Successfully converted Keystone template from v1beta2 to v1beta1")
	return nil
}
