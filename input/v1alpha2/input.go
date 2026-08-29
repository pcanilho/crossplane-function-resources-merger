// Package v1alpha2 contains the input type for this Function.
// +kubebuilder:object:generate=true
// +groupName=resources-merger.fn.canilho.net
// +versionName=v1alpha2
package v1alpha2

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// MergeStrategy selects how a source is merged onto the accumulator.
type MergeStrategy string

// Merge strategies supported by Input.MergeStrategy.
const (
	StrategyReplace                       MergeStrategy = "Replace"
	StrategyMergeObjects                  MergeStrategy = "MergeObjects"
	StrategyForceMergeObjects             MergeStrategy = "ForceMergeObjects"
	StrategyMergeObjectsAppendArrays      MergeStrategy = "MergeObjectsAppendArrays"
	StrategyForceMergeObjectsAppendArrays MergeStrategy = "ForceMergeObjectsAppendArrays"
)

// Resolution says whether a source must exist.
type Resolution string

// Resolution values supported by Source.Resolution.
const (
	ResolutionRequired Resolution = "Required"
	ResolutionOptional Resolution = "Optional"
)

// ResourceRef identifies one existing resource.
type ResourceRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	// Namespace is empty for cluster scoped kinds such as EnvironmentConfig.
	Namespace string `json:"namespace,omitempty"`
}

// TargetMetadata carries only what a merged resource needs. metav1.ObjectMeta
// would leak ownerReferences, finalizers and managedFields into the schema, and
// controller-gen renders it as an opaque object with no properties.
type TargetMetadata struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Target specifies the resource to create.
type Target struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	Name                       string `json:"name,omitempty"`
	NameFromCompositeFieldPath string `json:"nameFromCompositeFieldPath,omitempty"`

	Namespace                       string `json:"namespace,omitempty"`
	NamespaceFromCompositeFieldPath string `json:"namespaceFromCompositeFieldPath,omitempty"`

	// ToFieldPath defaults to data.
	ToFieldPath string          `json:"toFieldPath,omitempty"`
	Metadata    *TargetMetadata `json:"metadata,omitempty"`
}

// Source is one input to the merge. Order in Input.Sources is precedence order.
type Source struct {
	// Name is unique across sources. It is the required resource key.
	Name string      `json:"name"`
	Ref  ResourceRef `json:"ref"`

	// +kubebuilder:validation:Enum=Required;Optional
	Resolution Resolution `json:"resolution,omitempty"`

	// FromFieldPath defaults to data.
	FromFieldPath string `json:"fromFieldPath,omitempty"`

	// AllowCrossNamespace permits this source to be read from a namespace other
	// than the composite resource's own. The namespace must also appear in
	// Input.AllowedSourceNamespaces. Only meaningful when the composite is
	// namespaced; ignored when it is cluster scoped, where cross namespace
	// reads are already the norm.
	AllowCrossNamespace bool `json:"allowCrossNamespace,omitempty"`
}

// Input can be used to provide input to this Function.
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=crossplane
type Input struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Enum=Replace;MergeObjects;ForceMergeObjects;MergeObjectsAppendArrays;ForceMergeObjectsAppendArrays
	MergeStrategy MergeStrategy `json:"mergeStrategy,omitempty"`

	Target Target `json:"target"`

	// +kubebuilder:validation:MinItems=1
	Sources []Source `json:"sources"`

	// ParseEmbedded decodes string values that are YAML mappings so their
	// contents deep merge instead of being replaced wholesale.
	ParseEmbedded bool `json:"parseEmbedded,omitempty"`

	// AllowedSourceNamespaces lists the namespaces a source may be read from
	// besides the composite resource's own, when that source also sets
	// allowCrossNamespace. Ignored for a cluster scoped composite.
	AllowedSourceNamespaces []string `json:"allowedSourceNamespaces,omitempty"`

	Debug bool `json:"debug,omitempty"`
}
