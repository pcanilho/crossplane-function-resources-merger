// Package v1beta1 contains the input type for this Function.
// +kubebuilder:object:generate=true
// +groupName=merger.fn.canilho.net
// +versionName=v1beta1
package v1beta1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// MergeStrategy selects how a source is merged onto the accumulator.
type MergeStrategy string

// Merge strategies supported by Merge.MergeStrategy.
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
	Name       string `json:"name,omitempty"`

	// NameFromCompositeFieldPath resolves Name from a field path on the
	// composite resource, for example "spec.tenant". Mutually exclusive with
	// Name.
	//
	// There is deliberately no namespace equivalent. Namespace stays a static
	// string chosen by the Composition author, which bounds what a source can
	// ever be steered at.
	NameFromCompositeFieldPath string `json:"nameFromCompositeFieldPath,omitempty"`

	// Namespace is empty for cluster scoped kinds such as EnvironmentConfig.
	Namespace string `json:"namespace,omitempty"`
}

// Readiness is the readiness a composed target is reported with.
//
// There is deliberately no unset or Unspecified value.
// Ready_READY_UNSPECIFIED collapses to false rather than "unknown", which
// leaves the XR at Ready=False, Reason=Creating with an immediate requeue and
// an event per reconcile, and function-auto-ready does not rescue it.
type Readiness string

// Readiness values supported by Target.Readiness.
const (
	ReadinessTrue  Readiness = "True"
	ReadinessFalse Readiness = "False"
)

// TargetMetadata carries only what a merged resource needs. metav1.ObjectMeta
// would leak ownerReferences, finalizers and managedFields into the schema, and
// controller-gen renders it as an opaque object with no properties.
type TargetMetadata struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// TargetContext writes the merged result into the Composition context.
//
// It is an object rather than a bare string so it can gain fields without a
// breaking change, following the shape function-extra-resources settled on.
type TargetContext struct {
	// Key is the context key the merged result is written to.
	Key string `json:"key"`
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

	// StringifyScalars coerces a top-level non-string scalar to a string for a
	// ConfigMap or Secret target instead of failing. Off by default: the
	// coercion is one way, since an int written as "3" never returns as an int.
	// It exists for EnvironmentConfig and CR sources, whose data may legally
	// hold numbers. Every coerced key is named on the Merged condition.
	StringifyScalars bool `json:"stringifyScalars,omitempty"`

	// +kubebuilder:validation:Enum=True;False
	// Readiness defaults to True.
	Readiness Readiness `json:"readiness,omitempty"`

	// Context additionally writes the merged result into the Composition
	// context, for later pipeline steps. The composed resource is still
	// produced; this is an additional destination, not an alternative one.
	Context *TargetContext `json:"context,omitempty"`
}

// ParseFormat selects how a parsed value is re-encoded on output. It governs
// output serialization only: parsing accepts either format, since JSON is valid
// YAML. Re-encoding happens only for a ConfigMap or Secret target, where the
// merged data collapses to strings. For any other target kind the parsed
// value stays a native map and Format has no effect.
type ParseFormat string

// Parse formats supported by ParseSpec.Format.
const (
	// ParseFormatAuto re-encodes each value in the serialization it arrived in,
	// detected by its first non-whitespace byte.
	ParseFormatAuto ParseFormat = "Auto"
	ParseFormatYAML ParseFormat = "YAML"
	ParseFormatJSON ParseFormat = "JSON"
)

// ParseSpec scopes and shapes embedded-blob parsing for one source.
type ParseSpec struct {
	// +kubebuilder:validation:Enum=Auto;YAML;JSON
	// Format defaults to Auto. It is a no-op for any target that is not a
	// ConfigMap or Secret: only those two targets re-encode the merged data to
	// strings, so a value parsed for an EnvironmentConfig or CRD target keeps
	// its native map shape regardless of Format.
	Format ParseFormat `json:"format,omitempty"`

	// Keys lists the data keys to parse. Empty means every key, which is what
	// the Input-level parseEmbedded does.
	Keys []string `json:"keys,omitempty"`
}

// Source is one input to the merge.
//
// Order in Merge.Sources is fold order, and what that means for precedence
// depends on Merge.MergeStrategy. Under Replace, ForceMergeObjects and
// ForceMergeObjectsAppendArrays a later source overrides an earlier one. Under
// MergeObjects and MergeObjectsAppendArrays the accumulator wins, so an earlier
// source takes precedence and later sources only fill in absent keys.
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
	// Merge.AllowedSourceNamespaces. Only meaningful when the composite is
	// namespaced; ignored when it is cluster scoped, where cross namespace
	// reads are already the norm.
	AllowCrossNamespace bool `json:"allowCrossNamespace,omitempty"`

	// ToFieldPath places this source's contribution under a subtree of the
	// merged result instead of at its root. Dotted, for example
	// "teams.payments". Empty means the root, which is the default.
	//
	// This is placement, not precedence: the fold order and Merge.MergeStrategy
	// are unchanged.
	//
	// For a ConfigMap or Secret target, data is map[string]string, so a nested
	// subtree is not representable. It is YAML-encoded into a single string
	// value at the top-level key of this path.
	ToFieldPath string `json:"toFieldPath,omitempty"`

	// Parse scopes and shapes embedded-blob parsing for this source,
	// overriding the Input-level parseEmbedded. A source with no Parse block
	// follows parseEmbedded.
	//
	// When ToFieldPath is also set, the nested subtree collapses to one blob,
	// so per-key format detection is meaningless: Auto always re-encodes the
	// whole subtree as YAML, and only an explicit Format overrides it.
	Parse *ParseSpec `json:"parse,omitempty"`
}

// Merge describes one merge: which resources to read, how to combine them, and
// where to write the result.
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=crossplane
type Merge struct {
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
