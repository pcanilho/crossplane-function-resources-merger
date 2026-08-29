package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/pcanilho/crossplane-function-resources-merger/input/v1alpha2"
	"github.com/pcanilho/crossplane-function-resources-merger/internal/merger"
	"github.com/pcanilho/crossplane-function-resources-merger/internal/transformer"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/crossplane/crossplane-runtime/v2/pkg/errors"
	"github.com/crossplane/crossplane-runtime/v2/pkg/fieldpath"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	function "github.com/crossplane/function-sdk-go"
	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/resource/composed"
	"github.com/crossplane/function-sdk-go/response"
)

// defaultFieldPath is read from each source and written to the target when the
// Input does not name a path.
const defaultFieldPath = "data"

// Function returns whatever response you ask it to.
type Function struct {
	fnv1.UnimplementedFunctionRunnerServiceServer

	log logging.Logger
}

// resolvedSource pairs a declared source with the items Crossplane resolved for
// it. An empty items slice means Crossplane looked and found nothing.
type resolvedSource struct {
	source v1alpha2.Source
	items  []resource.Required
}

// RunFunction runs the Function. It never contacts the API server: it declares
// the resources it needs as requirements, Crossplane fetches them, and it
// returns the merged result as a desired composed resource.
func (f *Function) RunFunction(_ context.Context, req *fnv1.RunFunctionRequest) (*fnv1.RunFunctionResponse, error) {
	log := f.log
	log.Info("Running function", "tag", req.GetMeta().GetTag())

	// The merge reads resources that are not part of the response cache key, so
	// the response must not be cached.
	rsp := response.To(req, 0)

	// HasCapability returns false both when Crossplane lacks required resources
	// and when it predates capability advertisement entirely. AdvertisesCapabilities
	// distinguishes the two so an older Crossplane that does support required
	// resources is not wrongly rejected.
	if request.AdvertisesCapabilities(req) && !request.HasCapability(req, fnv1.Capability_CAPABILITY_REQUIRED_RESOURCES) {
		response.Fatal(rsp, errors.New("this Crossplane does not support required resources, which this function needs to resolve sources"))
		return rsp, nil
	}

	in := &v1alpha2.Input{}
	if err := request.GetInput(req, in); err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot get Function input from %T", req))
		return rsp, nil
	}

	if in.Debug {
		if debugLogger, err := function.NewLogger(true); err == nil {
			log = debugLogger.WithValues("tag", req.GetMeta().GetTag())
			log.Debug("Debug mode enabled")
		}
	}

	if err := validate(in); err != nil {
		response.Fatal(rsp, err)
		return rsp, nil
	}

	// Requirements are built on every call, resolved or not. Crossplane
	// compares the requirements a function returns against the ones it sent to
	// decide whether the function has converged.
	rsp.Requirements = &fnv1.Requirements{Resources: requirements(in)}

	oxr, err := request.GetObservedCompositeResource(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot get observed composite resource"))
		return rsp, nil
	}
	// A namespaced consumer XR would otherwise fail later inside Crossplane with
	// a message naming the composed resource rather than target.namespace.
	if ns := oxr.Resource.GetNamespace(); ns != "" {
		response.Fatal(rsp, errors.Errorf("the composite resource is namespaced %q; target.namespace requires a cluster scoped composite resource", ns))
		return rsp, nil
	}

	name, namespace, err := targetIdentity(oxr, in)
	if err != nil {
		response.Fatal(rsp, err)
		return rsp, nil
	}

	// Sources are walked in declared order. The requirement map is never
	// ranged: it is a Go map and the merge is an order dependent left fold.
	resolved := make([]resolvedSource, 0, len(in.Sources))
	for _, src := range in.Sources {
		items, ok, err := request.GetRequiredResource(req, src.Name)
		if err != nil {
			response.Fatal(rsp, errors.Wrapf(err, "cannot read required resource %q", src.Name))
			return rsp, nil
		}
		if !ok {
			// Crossplane has not looked for this source yet. Return the
			// requirements and no desired resources; Crossplane calls again.
			log.Debug("Waiting for Crossplane to resolve a source", "source", src.Name)
			return rsp, nil
		}
		resolved = append(resolved, resolvedSource{source: src, items: items})
	}

	merged, mergedCount, skipped, err := mergeSources(in, resolved)
	if err != nil {
		response.Fatal(rsp, err)
		return rsp, nil
	}

	gvk := schema.FromAPIVersionAndKind(in.Target.APIVersion, in.Target.Kind)
	switch {
	case isCoreV1Kind(gvk, "ConfigMap"):
		lowered, err := transformer.LowerToStringMap(merged)
		if err != nil {
			response.Fatal(rsp, errors.Wrap(err, "cannot lower merged data for a ConfigMap target"))
			return rsp, nil
		}
		merged = lowered
	case isCoreV1Kind(gvk, "Secret"):
		// Server-side apply never persists stringData, only data. Writing
		// stringData would leave the field manager owning a field absent from
		// storage, and keys removed from the merge would never be removed from
		// the Secret.
		encoded, err := secretData(merged)
		if err != nil {
			response.Fatal(rsp, errors.Wrap(err, "cannot encode merged data for a Secret target"))
			return rsp, nil
		}
		merged = encoded
	}

	msg := fmt.Sprintf("%d of %d sources merged", mergedCount, len(in.Sources))
	if len(skipped) > 0 {
		msg += "; skipped optional: " + strings.Join(skipped, ", ")
	}
	response.ConditionTrue(rsp, "Merged", "Success").TargetCompositeAndClaim().WithMessage(msg)

	cd, err := compose(in, gvk, name, namespace, merged)
	if err != nil {
		response.Fatal(rsp, err)
		return rsp, nil
	}

	// SetDesiredComposedResources in SDK v0.7.1 merges into the response rather
	// than replacing it, but its own doc comment makes the caller responsible
	// for not clobbering what earlier pipeline steps produced. Reading the
	// desired state and writing it back honours that contract whatever the SDK
	// does internally.
	desired, err := request.GetDesiredComposedResources(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot get desired composed resources"))
		return rsp, nil
	}
	key := desiredName(gvk.GroupKind(), namespace, name)
	desired[key] = &resource.DesiredComposed{Resource: cd, Ready: resource.ReadyTrue}
	if err := response.SetDesiredComposedResources(rsp, desired); err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot set desired composed resources"))
		return rsp, nil
	}

	log.Debug("Composed the merged resource", "name", string(key))
	return rsp, nil
}

// validate checks everything about an Input that does not need the request.
func validate(in *v1alpha2.Input) error {
	if in.Target.APIVersion == "" || in.Target.Kind == "" {
		return errors.New("no target resource group version kind")
	}
	if in.Target.Name != "" && in.Target.NameFromCompositeFieldPath != "" {
		return errors.New("target.name and target.nameFromCompositeFieldPath are mutually exclusive")
	}
	if in.Target.Name == "" && in.Target.NameFromCompositeFieldPath == "" {
		return errors.New("one of target.name or target.nameFromCompositeFieldPath is required")
	}
	if in.Target.Namespace != "" && in.Target.NamespaceFromCompositeFieldPath != "" {
		return errors.New("target.namespace and target.namespaceFromCompositeFieldPath are mutually exclusive")
	}
	if len(in.Sources) == 0 {
		return errors.New("no resources to merge")
	}
	if _, err := merger.Options(in.MergeStrategy); err != nil {
		return err
	}
	// A dot in Kind collides with the group separator in desiredName; a slash
	// collides with the namespace/name separator. Either breaks the derived
	// key's injectivity for the compose target.
	if strings.ContainsAny(in.Target.Kind, "./") {
		return errors.Errorf("target.kind %q must not contain \".\" or \"/\"", in.Target.Kind)
	}
	// schema.ParseGroupVersion silently returns an empty GroupVersion on a
	// parse error, so a malformed target.apiVersion would otherwise compose a
	// resource with an empty apiVersion instead of failing loudly here.
	if _, err := schema.ParseGroupVersion(in.Target.APIVersion); err != nil {
		return errors.Wrapf(err, "target.apiVersion %q is invalid", in.Target.APIVersion)
	}

	target := schema.FromAPIVersionAndKind(in.Target.APIVersion, in.Target.Kind)
	seen := make(map[string]bool, len(in.Sources))
	for _, src := range in.Sources {
		if src.Name == "" {
			return errors.New("every source needs a name; it is the required resource key")
		}
		// A source's name is the required resource key. Two sources sharing a
		// name would silently collapse into one requirement and merge slot.
		if seen[src.Name] {
			return errors.Errorf("duplicate source name %q; source names must be unique", src.Name)
		}
		seen[src.Name] = true
		if src.Ref.APIVersion == "" || src.Ref.Kind == "" {
			return errors.Errorf("source %q has no resource group version kind", src.Name)
		}
		if strings.ContainsAny(src.Ref.Kind, "./") {
			return errors.Errorf("source %q ref.kind %q must not contain \".\" or \"/\"", src.Name, src.Ref.Kind)
		}
		if src.Ref.Name == "" {
			return errors.Errorf("source %q has no resource name", src.Name)
		}
		// Secret data is base64. Merging it into a ConfigMap would emit garbage
		// and write the secret material out in plaintext.
		if isCoreV1Kind(schema.FromAPIVersionAndKind(src.Ref.APIVersion, src.Ref.Kind), "Secret") && !isCoreV1Kind(target, "Secret") {
			return errors.Errorf("source %q is a Secret; a Secret source requires a Secret target", src.Name)
		}
	}
	return nil
}

// requirements is one ResourceSelector per source, keyed by the source's name.
func requirements(in *v1alpha2.Input) map[string]*fnv1.ResourceSelector {
	out := make(map[string]*fnv1.ResourceSelector, len(in.Sources))
	for _, src := range in.Sources {
		sel := &fnv1.ResourceSelector{
			ApiVersion: src.Ref.APIVersion,
			Kind:       src.Ref.Kind,
			Match:      &fnv1.ResourceSelector_MatchName{MatchName: src.Ref.Name},
		}
		// Namespace is a *string. nil is how a cluster scoped kind such as
		// EnvironmentConfig is expressed; a pointer to "" is not the same.
		if src.Ref.Namespace != "" {
			ns := src.Ref.Namespace
			sel.Namespace = &ns
		}
		out[src.Name] = sel
	}
	return out
}

// targetIdentity resolves the target's name and namespace. Field paths are read
// from the observed composite resource: the Composition author names the path,
// and no XR spec field is ever hardcoded here.
func targetIdentity(oxr *resource.Composite, in *v1alpha2.Input) (string, string, error) {
	name := in.Target.Name
	namespace := in.Target.Namespace

	if in.Target.NameFromCompositeFieldPath == "" && in.Target.NamespaceFromCompositeFieldPath == "" {
		return name, namespace, nil
	}

	if p := in.Target.NameFromCompositeFieldPath; p != "" {
		v, err := oxr.Resource.GetString(p)
		if err != nil {
			return "", "", errors.Wrapf(err, "cannot resolve target.nameFromCompositeFieldPath %q", p)
		}
		if v == "" {
			return "", "", errors.Errorf("target.nameFromCompositeFieldPath %q resolved to an empty name", p)
		}
		name = v
	}

	if p := in.Target.NamespaceFromCompositeFieldPath; p != "" {
		v, err := oxr.Resource.GetString(p)
		if err != nil {
			return "", "", errors.Wrapf(err, "cannot resolve target.namespaceFromCompositeFieldPath %q", p)
		}
		if v == "" {
			// An empty namespace would silently make the target cluster scoped
			// and drop the namespace from the desired state key.
			return "", "", errors.Errorf("target.namespaceFromCompositeFieldPath %q resolved to an empty namespace", p)
		}
		namespace = v
	}

	return name, namespace, nil
}

// mergeSources folds the resolved sources in declared order. It also reports
// how many sources contributed data and the names of any Optional sources that
// resolved to nothing, for the Merged condition.
func mergeSources(in *v1alpha2.Input, resolved []resolvedSource) (merged map[string]any, mergedCount int, skipped []string, err error) {
	merged = map[string]any{}
	folded := false

	for _, rs := range resolved {
		src := rs.source
		if len(rs.items) == 0 {
			// Crossplane looked and the source does not exist.
			if src.Resolution == v1alpha2.ResolutionOptional {
				skipped = append(skipped, src.Name)
				continue
			}
			return nil, 0, nil, errors.Errorf("source %q is required but %s %q does not exist", src.Name, src.Ref.Kind, src.Ref.Name)
		}
		mergedCount++

		for _, item := range rs.items {
			data, err := sourceData(src, item)
			if err != nil {
				return nil, 0, nil, err
			}
			if in.ParseEmbedded {
				data = transformer.Parse(data)
			}
			if !folded {
				// The accumulator starts as the first source, normalized
				// rather than merged: transformer.Parse can produce
				// map[any]any for a blob with non-string keys, which
				// Normalize's deep copy folds into map[string]any. Merge is
				// not used here because its mergo path silently drops a
				// top-level explicit-nil key under MergeObjects and
				// MergeObjectsAppendArrays even when dst has no such key.
				merged = merger.Normalize(data)
				folded = true
				continue
			}
			merged, err = merger.Merge(merged, data, in.MergeStrategy)
			if err != nil {
				return nil, 0, nil, errors.Wrapf(err, "cannot merge source %q", src.Name)
			}
		}
	}

	return merged, mergedCount, skipped, nil
}

// sourceData reads a source's fromFieldPath and insists it is an object. An
// unchecked assertion here would panic on data that is null or a list, which
// would kill the shared function pod for every XR using this function.
func sourceData(src v1alpha2.Source, item resource.Required) (map[string]any, error) {
	path := src.FromFieldPath
	if path == "" {
		path = defaultFieldPath
	}

	v, err := fieldpath.Pave(item.Resource.Object).GetValue(path)
	if err != nil {
		return nil, errors.Wrapf(err, "source %q has no field %q", src.Name, path)
	}

	data, ok := v.(map[string]any)
	if !ok {
		return nil, errors.Errorf("source %q field %q is %T, want an object", src.Name, path, v)
	}

	// A Secret's data values are base64 by API contract. Decoding here keeps
	// the merge itself kind-agnostic, operating on plaintext exactly as it does
	// for a ConfigMap source.
	if isCoreV1Kind(schema.FromAPIVersionAndKind(src.Ref.APIVersion, src.Ref.Kind), "Secret") {
		return decodeSecretData(src.Name, data)
	}
	return data, nil
}

// decodeSecretData base64-decodes every value read from a Secret source. A
// value that fails to decode is Fatal rather than passed through as plaintext,
// since a Secret's data is base64 by contract and guessing risks wrong secret
// material with no signal to the operator.
func decodeSecretData(name string, data map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(data))
	for k, v := range data {
		s, ok := v.(string)
		if !ok {
			return nil, errors.Errorf("source %q key %q is %T, want a base64 string", name, k, v)
		}
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, errors.Wrapf(err, "source %q key %q is not valid base64", name, k)
		}
		out[k] = string(b)
	}
	return out, nil
}

// compose builds the desired composed resource.
func compose(in *v1alpha2.Input, gvk schema.GroupVersionKind, name, namespace string, data map[string]any) (*composed.Unstructured, error) {
	cd := composed.New()
	cd.SetGroupVersionKind(gvk)
	cd.SetName(name)
	if namespace != "" {
		cd.SetNamespace(namespace)
	}

	if m := in.Target.Metadata; m != nil {
		if len(m.Labels) > 0 {
			cd.SetLabels(m.Labels)
		}
		if len(m.Annotations) > 0 {
			cd.SetAnnotations(m.Annotations)
		}
	}

	path := in.Target.ToFieldPath
	if path == "" {
		path = defaultFieldPath
	}
	if err := cd.SetValue(path, data); err != nil {
		return nil, errors.Wrapf(err, "cannot set target field %q", path)
	}
	return cd, nil
}

// secretData lowers merged data to strings, as for a ConfigMap, then
// base64-encodes each value for a Secret's data field.
func secretData(merged map[string]any) (map[string]any, error) {
	lowered, err := transformer.LowerToStringMap(merged)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(lowered))
	for k, v := range lowered {
		s, ok := v.(string)
		if !ok {
			return nil, errors.Errorf("key %q lowered to %T, want a string", k, v)
		}
		out[k] = base64.StdEncoding.EncodeToString([]byte(s))
	}
	return out, nil
}

// desiredName derives the desired state map key from the target's identity:
// <kind>[.<group>]/[<namespace>/]<name>, kind lowercased. Keying on
// target.name means a rename drops the old entry, so Crossplane deletes and
// recreates rather than moving data. The version is excluded because one
// group and kind at two versions is one object. The separator is a slash, not
// a dot, because a name may itself contain dots; see TestDesiredNameIsInjective.
func desiredName(gk schema.GroupKind, namespace, name string) resource.Name {
	// Only the kind is lowercased. A group, a namespace and a name are already
	// lowercase per API validation, and folding their case here would merge two
	// distinct identities onto one key.
	id := strings.ToLower(gk.Kind)
	if gk.Group != "" {
		id += "." + gk.Group
	}
	if namespace == "" {
		return resource.Name(id + "/" + name)
	}
	return resource.Name(id + "/" + namespace + "/" + name)
}

// isCoreV1Kind reports whether gvk is the supplied kind in the core v1 group.
func isCoreV1Kind(gvk schema.GroupVersionKind, kind string) bool {
	return gvk.Group == "" && gvk.Version == "v1" && gvk.Kind == kind
}
