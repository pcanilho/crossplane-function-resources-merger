// Package v1alpha1 contains the input type for this Function
// +kubebuilder:object:generate=true
// +groupName=resources-merger.fn.canilho.net
// +versionName=v1alpha1
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

// This isn't a custom resource, in the sense that we never install its CRD.
// It is a KRM-like object, so we generate a CRD to describe its schema.

// ResourceRef is a reference to a Kubernetes resource.
type ResourceRef struct {
	Ref            v1.TypedReference `json:",inline"`
	Namespace      string            `json:"namespace,omitempty"`
	ExtractFromKey string            `json:"extractFromKey,omitempty"`
}

// Input can be used to provide input to this Function.
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=crossplane
type Input struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Debug        bool          `json:"debug,omitempty"`
	TargetRef    ResourceRef   `json:"targetRef"`
	ResourceRefs []ResourceRef `json:"resourceRefs"`
}
