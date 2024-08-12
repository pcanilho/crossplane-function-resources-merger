// Package v1alpha1 contains the input type for this Function
// +kubebuilder:object:generate=true
// +groupName=resources-merger.fn.canilho.net
// +versionName=v1alpha1
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

// SourceRef is a reference to a Kubernetes resource.
type SourceRef struct {
	Ref            v1.TypedReference `json:",inline"`
	Namespace      string            `json:"namespace,omitempty"`
	ExtractFromKey string            `json:"extractFromKey,omitempty"`
	Key            string            `json:"key,omitempty"`
}

// Input can be used to provide input to this Function.
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=crossplane
type Input struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Debug      bool        `json:"debug,omitempty"`
	TargetRef  SourceRef   `json:"targetRef"`
	SourceRefs []SourceRef `json:"sourceRefs"`
}
