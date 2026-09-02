package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pcanilho/crossplane-function-resources-merger/input/v1beta1"
	"github.com/pcanilho/crossplane-function-resources-merger/internal/merger"
	"github.com/pcanilho/crossplane-function-resources-merger/internal/transformer"
	"google.golang.org/protobuf/types/known/structpb"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

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

// requirementPrefix stops a Composition author's own requirementName from
// colliding with this function's requirement keys.
const requirementPrefix = "merger.fn.canilho.net/"

// requirementKey is the key a source is requested and read back under. Source
// names stay unprefixed everywhere else: Merge, errors, conditions.
func requirementKey(sourceName string) string { return requirementPrefix + sourceName }

// Function returns whatever response you ask it to.
type Function struct {
	fnv1.UnimplementedFunctionRunnerServiceServer

	log logging.Logger
}

// resolvedSource pairs a declared source with the items Crossplane resolved for
// it. An empty items slice means Crossplane looked and found nothing.
type resolvedSource struct {
	source v1beta1.Source
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

	in := &v1beta1.Merge{}
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

	oxr, err := request.GetObservedCompositeResource(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot get observed composite resource"))
		return rsp, nil
	}
	xrNamespace := oxr.Resource.GetNamespace()

	// Computed once and reused everywhere the target's GVK is needed: the
	// cluster scoped target check below, the misdirected Secret check further
	// down, the ConfigMap/Secret data shaping, compose, and the desired state
	// key. validate has already confirmed the apiVersion parses.
	gvkTarget := schema.FromAPIVersionAndKind(in.Target.APIVersion, in.Target.Kind)
	if err := clusterScopedTargetGuard(gvkTarget, xrNamespace); err != nil {
		response.Fatal(rsp, err)
		return rsp, nil
	}

	// Requirements are built on every call, resolved or not. Crossplane
	// compares the requirements a function returns against the ones it sent to
	// decide whether the function has converged. They are built after the
	// composite is read because the confinement guard needs its namespace and
	// ref.nameFromCompositeFieldPath needs its spec.
	selectors, problems, crossedNamespaces, resolvedNames := requirements(in, oxr, xrNamespace)
	rsp.Requirements = &fnv1.Requirements{Resources: selectors}
	if len(problems) > 0 {
		// Aggregating is not the usual choice, but these are all static Input
		// errors surfaced together: reporting one per reconcile makes the
		// author fix N misconfigurations in N cycles.
		response.Fatal(rsp, errors.Join(problems...))
		return rsp, nil
	}

	// One rename site. requirements() renamed its own copies for the selector
	// it emitted; every consumer from here on reads this slice, never
	// in.Sources, so a source's ref.name is the resource actually requested.
	sources := renamedSources(in.Sources, resolvedNames)

	warnNoOpCrossNamespaceFlags(rsp, sources)

	name, namespace, err := targetIdentity(oxr, in, xrNamespace)
	if err != nil {
		response.Fatal(rsp, err)
		return rsp, nil
	}

	// Under a namespaced composite Crossplane pins the composed resource to the
	// XR's namespace. Leave the composed resource's namespace empty: that is
	// what arms Crossplane's IsObjectNamespaced check, which is gated on the
	// field being empty. Setting it lets a cluster scoped target be created and
	// then permanently orphaned, since a cluster scoped object owned by a
	// namespaced XR can never be garbage collected.
	composeNamespace, keyNamespace, ignoredTargetNamespace, err := namespacePinning(gvkTarget, xrNamespace, namespace)
	if err != nil {
		response.Fatal(rsp, err)
		return rsp, nil
	}
	if ignoredTargetNamespace != "" {
		response.Warning(rsp, errors.Errorf("target.namespace %q is ignored: a namespaced composite pins the target to its own namespace %q", ignoredTargetNamespace, xrNamespace)).
			TargetCompositeAndClaim()
	}

	// Sources are walked in declared order. The requirement map is never
	// ranged: it is a Go map and the merge is an order dependent left fold.
	resolved := make([]resolvedSource, 0, len(sources))
	for _, src := range sources {
		items, ok, err := request.GetRequiredResource(req, requirementKey(src.Name))
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

	merged, formats, mergedCount, skipped, noData, err := mergeSources(in, resolved)
	if err != nil {
		response.Fatal(rsp, err)
		return rsp, nil
	}

	// Below the GetRequiredResource wait loop above, so an unresolved source can
	// never reach this point and produce a half-built context value. Written
	// from the merged result before ConfigMap/Secret coercion, so context gets
	// the natural typed value regardless of the target's Kind: a number stays a
	// number, a nested object stays an object, and a Secret target does not
	// force every consumer to base64-decode it.
	if c := in.Target.Context; c != nil {
		v, err := structpb.NewStruct(merged)
		if err != nil {
			response.Fatal(rsp, errors.Wrapf(err, "cannot encode the merged result for context key %q", c.Key))
			return rsp, nil
		}
		response.SetContextKey(rsp, c.Key, structpb.NewStructValue(v))
	}

	var coerced []string
	switch {
	case isCoreV1Kind(gvkTarget, "ConfigMap"):
		lowered, c, err := transformer.LowerToStringMapWith(merged, formats, in.Target.StringifyScalars)
		if err != nil {
			response.Fatal(rsp, errors.Wrap(err, "cannot lower merged data for a ConfigMap target"))
			return rsp, nil
		}
		merged = lowered
		coerced = c
	case isCoreV1Kind(gvkTarget, "Secret"):
		// Server-side apply never persists stringData, only data. Writing
		// stringData would leave the field manager owning a field absent from
		// storage, and keys removed from the merge would never be removed from
		// the Secret.
		encoded, c, err := secretData(merged, formats, in.Target.StringifyScalars)
		if err != nil {
			response.Fatal(rsp, errors.Wrap(err, "cannot encode merged data for a Secret target"))
			return rsp, nil
		}
		merged = encoded
		coerced = c
	}

	msg := mergedMessage(mergedReport{
		mergedCount:            mergedCount,
		sources:                sources,
		skipped:                skipped,
		noData:                 noData,
		crossedNamespaces:      crossedNamespaces,
		resolvedNames:          resolvedNames,
		xrNamespace:            xrNamespace,
		ignoredTargetNamespace: ignoredTargetNamespace,
		coerced:                coerced,
	})
	// A merge that contributed nothing is not a success. Not fatal: an absent
	// field is benign by design, and Merged is a custom type Crossplane drops from XR readiness.
	if mergedCount == 0 {
		response.ConditionFalse(rsp, "Merged", "NoData").TargetCompositeAndClaim().WithMessage(msg)
	} else {
		response.ConditionTrue(rsp, "Merged", "Success").TargetCompositeAndClaim().WithMessage(msg)
	}

	cd, ready, err := compose(in, gvkTarget, name, composeNamespace, merged)
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
	key := desiredName(gvkTarget.GroupKind(), keyNamespace, name)
	if _, taken := desired[key]; taken {
		// Silently replacing it would discard an earlier step's resource with no signal.
		response.Fatal(rsp, errors.Errorf("an earlier pipeline step already produced a composed resource under key %q; this function will not overwrite it", key))
		return rsp, nil
	}
	desired[key] = &resource.DesiredComposed{Resource: cd, Ready: ready}
	if err := response.SetDesiredComposedResources(rsp, desired); err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot set desired composed resources"))
		return rsp, nil
	}

	log.Debug("Composed the merged resource", "name", string(key))
	return rsp, nil
}

// clusterScopedTargetGuard rejects a target kind known to be cluster scoped
// when the composite is namespaced. A namespaced composite can never own a
// cluster scoped resource: it would be permanently orphaned, since a cluster
// scoped object owned by a namespaced XR cannot be garbage collected.
func clusterScopedTargetGuard(gvkTarget schema.GroupVersionKind, xrNamespace string) error {
	if xrNamespace == "" {
		return nil
	}
	gk := gvkTarget.GroupKind()
	if !isClusterScopedTarget(gk) {
		return nil
	}
	return errors.Errorf("target %s/%s is cluster scoped and cannot be composed by a composite in namespace %q", gk.Group, gk.Kind, xrNamespace)
}

// warnNoOpCrossNamespaceFlags warns on each source whose allowCrossNamespace is
// set but can have no effect, because an empty ref.namespace already means a
// cluster scoped kind.
func warnNoOpCrossNamespaceFlags(rsp *fnv1.RunFunctionResponse, sources []v1beta1.Source) {
	for _, src := range sources {
		if src.AllowCrossNamespace && src.Ref.Namespace == "" {
			response.Warning(rsp, errors.Errorf("allowCrossNamespace on source %q has no effect: an empty ref.namespace already means a cluster scoped kind", src.Name)).
				TargetCompositeAndClaim()
		}
	}
}

// namespacePinning derives the compose and desired-state-key namespaces from
// the target's resolved namespace, and whether target.namespace conflicts with
// a namespaced composite's pinning.
//
// Under a namespaced composite Crossplane pins the composed resource to the
// XR's namespace. composeNamespace stays empty: that is what arms Crossplane's
// IsObjectNamespaced check, which is gated on the field being empty. Setting it
// would let a cluster scoped target be created and then permanently orphaned.
// keyNamespace tracks the pinned namespace so the desired state key matches
// where the resource actually lands.
//
// ignoredTargetNamespace is set, and err is nil, when target.namespace names a
// different namespace than the one the composite pins to: the composite wins
// and the caller should warn. err is set instead of ignoredTargetNamespace when
// the target is a Secret, since relocating secret material silently would be
// worse than refusing.
func namespacePinning(gvkTarget schema.GroupVersionKind, xrNamespace, namespace string) (composeNamespace, keyNamespace, ignoredTargetNamespace string, err error) {
	if xrNamespace == "" {
		return namespace, namespace, "", nil
	}
	if namespace == "" || namespace == xrNamespace {
		return "", xrNamespace, "", nil
	}
	if isCoreV1Kind(gvkTarget, "Secret") {
		return "", "", "", errors.Errorf("target.namespace %q cannot be honoured: a namespaced composite pins the target to %q, which would relocate secret material", namespace, xrNamespace)
	}
	return "", xrNamespace, namespace, nil
}

// mergedReport is everything the Merged condition's message is assembled from.
// A struct rather than a parameter list: the clauses only ever grow, and a
// positional call of eight same-typed arguments is easy to transpose silently.
type mergedReport struct {
	mergedCount int
	// sources is the renamed slice, so ref.name is what was requested.
	sources                []v1beta1.Source
	skipped                []string
	noData                 []string
	crossedNamespaces      []v1beta1.Source
	resolvedNames          map[string]string
	xrNamespace            string
	ignoredTargetNamespace string
	// coerced names every key stringifyScalars coerced from a non-string
	// scalar, sorted by the transformer for a deterministic message.
	coerced []string
}

// mergedMessage assembles the Merged condition's message from its optional
// clauses, in the fixed order: base count, skipped optional sources, sources
// whose field was absent, cross-namespace audit, field-path-resolved names,
// ignored target.namespace, coerced scalars.
func mergedMessage(r mergedReport) string {
	msg := fmt.Sprintf("%d of %d sources merged", r.mergedCount, len(r.sources))
	if len(r.skipped) > 0 {
		msg += "; skipped optional: " + strings.Join(r.skipped, ", ")
	}
	if len(r.noData) > 0 {
		msg += "; no data: " + strings.Join(r.noData, ", ")
	}
	if len(r.crossedNamespaces) > 0 {
		notes := make([]string, 0, len(r.crossedNamespaces))
		for _, src := range r.crossedNamespaces {
			notes = append(notes, fmt.Sprintf("%s -> %s/%s in %s", src.Name, src.Ref.Kind, src.Ref.Name, src.Ref.Namespace))
		}
		msg += "; cross-namespace: " + strings.Join(notes, ", ")
	}
	if len(r.resolvedNames) > 0 {
		// Declared source order, not map order: the message is persisted on the
		// composite and must not flap between reconciles.
		names := make([]string, 0, len(r.resolvedNames))
		for _, src := range r.sources {
			if v, ok := r.resolvedNames[src.Name]; ok {
				names = append(names, fmt.Sprintf("%s -> %s", src.Name, v))
			}
		}
		msg += "; resolved names: " + strings.Join(names, ", ")
	}
	if r.ignoredTargetNamespace != "" {
		msg += fmt.Sprintf("; ignored target.namespace: %q, using %q", r.ignoredTargetNamespace, r.xrNamespace)
	}
	if len(r.coerced) > 0 {
		msg += "; coerced: " + strings.Join(r.coerced, ", ")
	}
	return msg
}

// renamedSources returns sources with every field-path-resolved ref.name
// applied. The input is never mutated: it is the Input the caller parsed.
func renamedSources(sources []v1beta1.Source, resolvedNames map[string]string) []v1beta1.Source {
	if len(resolvedNames) == 0 {
		return sources
	}
	out := make([]v1beta1.Source, len(sources))
	copy(out, sources)
	for i := range out {
		if v, ok := resolvedNames[out[i].Name]; ok {
			out[i].Ref.Name = v
		}
	}
	return out
}

// validate checks everything about an Input that does not need the request.
func validate(in *v1beta1.Merge) error {
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

	if slices.Contains(in.AllowedSourceNamespaces, "") {
		return errors.New("allowedSourceNamespaces must not contain an empty namespace")
	}
	if in.Target.Context != nil && in.Target.Context.Key == "" {
		return errors.New("target.context.key is required when target.context is set")
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
		if src.Ref.Name != "" && src.Ref.NameFromCompositeFieldPath != "" {
			return errors.Errorf("source %q sets both ref.name and ref.nameFromCompositeFieldPath; they are mutually exclusive", src.Name)
		}
		if src.Ref.Name == "" && src.Ref.NameFromCompositeFieldPath == "" {
			return errors.Errorf("source %q has no resource name; set ref.name or ref.nameFromCompositeFieldPath", src.Name)
		}
		// allowedSourceNamespaces pins a namespace, not a name. It was written
		// for a statically named source, where the admin pinned both. A
		// composite-supplied name in a permitted foreign namespace would select
		// any resource of this kind there, which is the read the pair exists to
		// confine.
		if src.Ref.NameFromCompositeFieldPath != "" && src.AllowCrossNamespace {
			return errors.Errorf("source %q sets both ref.nameFromCompositeFieldPath and allowCrossNamespace; they cannot be combined, because allowedSourceNamespaces pins a namespace but not a name, so the composite could name any %s in the permitted namespace", src.Name, src.Ref.Kind)
		}
		// Secret data is base64. Merging it into a ConfigMap would emit garbage
		// and write the secret material out in plaintext.
		if isCoreV1Kind(schema.FromAPIVersionAndKind(src.Ref.APIVersion, src.Ref.Kind), "Secret") && !isCoreV1Kind(target, "Secret") {
			return errors.Errorf("source %q is a Secret; a Secret source requires a Secret target", src.Name)
		}
	}
	return nil
}

// crossNamespaceViolation reports why a source may not be read, or nil if it
// may. Confinement applies only to a namespaced composite: a cluster scoped one
// reads across namespaces by design.
//
// resolution does not soften this. A violation is a configuration error, not a
// missing resource.
func crossNamespaceViolation(in *v1beta1.Merge, src v1beta1.Source, xrNamespace string) error {
	if xrNamespace == "" || src.Ref.Namespace == "" || src.Ref.Namespace == xrNamespace {
		return nil
	}
	if !src.AllowCrossNamespace {
		return errors.Errorf("source %q reads namespace %q but the composite is in %q; set allowCrossNamespace and list the namespace in allowedSourceNamespaces to permit this", src.Name, src.Ref.Namespace, xrNamespace)
	}
	if slices.Contains(in.AllowedSourceNamespaces, src.Ref.Namespace) {
		return nil
	}
	return errors.Errorf("source %q reads namespace %q, which is not in allowedSourceNamespaces", src.Name, src.Ref.Namespace)
}

// requirements is one ResourceSelector per source, keyed by the source's name.
// A source whose name cannot be resolved, or whose read would cross namespaces
// without permission, is omitted from the map entirely; the caller reports the
// accumulated problems. crossed lists, in declared order, every source
// permitted to read another namespace, for the Merged condition's
// cross-namespace audit trail. resolvedNames maps a source name to the resource
// name a field path produced for it, for the same condition's audit trail.
//
// ref.nameFromCompositeFieldPath is resolved here, and nowhere later, because
// this is where the read is asked for. By the time a source reaches
// mergeSources Crossplane has already fetched it under its own cluster-wide
// ServiceAccount, so a guard there would block the merge, not the read.
func requirements(in *v1beta1.Merge, oxr *resource.Composite, xrNamespace string) (out map[string]*fnv1.ResourceSelector, problems []error, crossed []v1beta1.Source, resolvedNames map[string]string) {
	out = make(map[string]*fnv1.ResourceSelector, len(in.Sources))
	resolvedNames = map[string]string{}

	for _, src := range in.Sources {
		var resolvedName string
		if p := src.Ref.NameFromCompositeFieldPath; p != "" {
			name, err := resolveSourceName(oxr, src.Name, p)
			if err != nil {
				problems = append(problems, err)
				continue
			}
			// src is a per-iteration copy, so this renames the source only for
			// the selector and the cross-namespace audit trail built below.
			src.Ref.Name = name
			resolvedName = name
		}

		if err := crossNamespaceViolation(in, src, xrNamespace); err != nil {
			// Omit the selector entirely. Crossplane does check for fatal
			// results before acting on requirements, but that ordering is
			// internal to Crossplane, not a guarantee of the proto.
			problems = append(problems, err)
			continue
		}

		// Recorded only once the source survives every guard, so the audit
		// trail lists what was requested, not what was resolved then rejected.
		if resolvedName != "" {
			resolvedNames[src.Name] = resolvedName
		}

		if xrNamespace != "" && src.Ref.Namespace != "" && src.Ref.Namespace != xrNamespace {
			crossed = append(crossed, src)
		}

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
		out[requirementKey(src.Name)] = sel
	}
	return out, problems, crossed, resolvedNames
}

// resolveSourceName reads one source's resource name from a field path on the
// observed composite. Every failure is an error rather than a fallback: the
// caller omits the selector, so an unresolved name is never requested.
func resolveSourceName(oxr *resource.Composite, sourceName, path string) (string, error) {
	v, err := oxr.Resource.GetString(path)
	if err != nil {
		return "", errors.Wrapf(err, "source %q: cannot resolve ref.nameFromCompositeFieldPath %q", sourceName, path)
	}
	if v == "" {
		return "", errors.Errorf("source %q: ref.nameFromCompositeFieldPath %q resolved to an empty name", sourceName, path)
	}
	if err := validateResourceName(v); err != nil {
		return "", errors.Wrapf(err, "source %q: ref.nameFromCompositeFieldPath %q resolved to %q", sourceName, path, v)
	}
	return v, nil
}

// validateResourceName rejects a resolved name that is not a DNS subdomain.
// Applied to every field path that produces a name: for a target it keeps
// desiredName injective, whose keys separate on "/", and for a source it keeps
// the selector to names the API server could hold.
func validateResourceName(name string) error {
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return errors.Errorf("not a valid resource name: %s", strings.Join(errs, "; "))
	}
	return nil
}

// targetIdentity resolves the target's name and namespace. Field paths are read
// from the observed composite resource: the Composition author names the path,
// and no XR spec field is ever hardcoded here.
func targetIdentity(oxr *resource.Composite, in *v1beta1.Merge, xrNamespace string) (string, string, error) {
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
		// This is the field path that actually feeds desiredName, whose
		// injectivity rests on a name being a DNS subdomain.
		if err := validateResourceName(v); err != nil {
			return "", "", errors.Wrapf(err, "target.nameFromCompositeFieldPath %q resolved to %q", p, v)
		}
		name = v
	}

	if p := in.Target.NamespaceFromCompositeFieldPath; p != "" {
		v, err := oxr.Resource.GetString(p)
		if err != nil {
			return "", "", errors.Wrapf(err, "cannot resolve target.namespaceFromCompositeFieldPath %q", p)
		}
		if v == "" && xrNamespace == "" {
			// An empty namespace would silently make the target cluster scoped
			// and drop the namespace from the desired state key. Under a
			// namespaced composite the resolved value is discarded anyway, so
			// an empty resolution is harmless there.
			return "", "", errors.Errorf("target.namespaceFromCompositeFieldPath %q resolved to an empty namespace", p)
		}
		namespace = v
	}

	return name, namespace, nil
}

// mergeSources folds the resolved sources in declared order. It also reports
// how many sources contributed data, the names of any Optional sources that
// resolved to nothing, and the names of any sources that resolved but whose
// field was absent, for the Merged condition.
func mergeSources(in *v1beta1.Merge, resolved []resolvedSource) (merged map[string]any, formats map[string]v1beta1.ParseFormat, mergedCount int, skipped, noData []string, err error) {
	merged = map[string]any{}
	formats = map[string]v1beta1.ParseFormat{}
	folded := false

	for _, rs := range resolved {
		src := rs.source
		if len(rs.items) == 0 {
			// Crossplane looked and the source does not exist.
			if src.Resolution == v1beta1.ResolutionOptional {
				skipped = append(skipped, src.Name)
				continue
			}
			where := fmt.Sprintf("namespace %q", src.Ref.Namespace)
			if src.Ref.Namespace == "" {
				where = "the cluster scope"
			}
			return nil, nil, 0, nil, nil, errors.Errorf("source %q is required but %s %q does not exist in %s", src.Name, src.Ref.Kind, src.Ref.Name, where)
		}

		contributed := false
		for _, item := range rs.items {
			data, absent, err := sourceData(src, item)
			if err != nil {
				return nil, nil, 0, nil, nil, err
			}
			if absent {
				// The resource exists but fromFieldPath does not. Contributes
				// nothing; not an error under either resolution.
				continue
			}
			contributed = true
			switch {
			case src.Parse != nil:
				var origin map[string]v1beta1.ParseFormat
				data, origin = transformer.ParseWith(data, src.Parse.Keys)
				if src.ToFieldPath == "" {
					// A nested key never surfaces at merged's top level for
					// this source, so recording it here would only risk
					// clobbering an unrelated sibling source's own key.
					maps.Copy(formats, origin)
				}
			case in.ParseEmbedded:
				data = transformer.Parse(data)
			}
			data = nest(src.ToFieldPath, data)
			if src.Parse != nil && src.Parse.Format != "" && src.Parse.Format != v1beta1.ParseFormatAuto {
				for k := range data {
					formats[k] = src.Parse.Format
				}
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
				return nil, nil, 0, nil, nil, errors.Wrapf(err, "cannot merge source %q", src.Name)
			}
		}
		if contributed {
			mergedCount++
		} else {
			noData = append(noData, src.Name)
		}
	}

	return merged, formats, mergedCount, skipped, noData, nil
}

// nest wraps data in the subtree named by a dotted path. Runs before the fold,
// so the merge strategy applies to the nested shape.
func nest(path string, data map[string]any) map[string]any {
	if path == "" {
		return data
	}
	segments := strings.Split(path, ".")
	out := data
	for _, segment := range slices.Backward(segments) {
		out = map[string]any{segment: out}
	}
	return out
}

// sourceData reads a source's fromFieldPath and insists it is an object. An
// unchecked assertion here would panic on data that is null or a list, which
// would kill the shared function pod for every XR using this function.
//
// absent reports a field that does not exist on a resource that does
// (fieldpath.IsNotFound): the source contributes nothing, not an error. Any
// other GetValue failure, and a field that resolves to something other than
// an object, is a static Input bug and remains fatal.
func sourceData(src v1beta1.Source, item resource.Required) (data map[string]any, absent bool, err error) {
	path := src.FromFieldPath
	if path == "" {
		path = defaultFieldPath
	}

	v, err := fieldpath.Pave(item.Resource.Object).GetValue(path)
	if err != nil {
		if fieldpath.IsNotFound(err) {
			return nil, true, nil
		}
		return nil, false, errors.Wrapf(err, "cannot read source %q field %q", src.Name, path)
	}

	m, ok := v.(map[string]any)
	if !ok {
		return nil, false, errors.Errorf("source %q field %q is %T, want an object", src.Name, path, v)
	}

	// A Secret's data values are base64 by API contract. Decoding here keeps
	// the merge itself kind-agnostic, operating on plaintext exactly as it does
	// for a ConfigMap source.
	if isCoreV1Kind(schema.FromAPIVersionAndKind(src.Ref.APIVersion, src.Ref.Kind), "Secret") {
		decoded, err := decodeSecretData(src.Name, m)
		return decoded, false, err
	}
	return m, false, nil
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

// compose builds the desired composed resource, along with the readiness it
// should be reported with. Readiness defaults to true; target.readiness:
// False is the only way to report false, since Ready_READY_UNSPECIFIED
// collapses to false in Crossplane and must never be exposed here.
func compose(in *v1beta1.Merge, gvk schema.GroupVersionKind, name, namespace string, data map[string]any) (*composed.Unstructured, resource.Ready, error) {
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
		return nil, resource.ReadyUnspecified, errors.Wrapf(err, "cannot set target field %q", path)
	}

	ready := resource.ReadyTrue
	if in.Target.Readiness == v1beta1.ReadinessFalse {
		ready = resource.ReadyFalse
	}
	return cd, ready, nil
}

// secretData lowers merged data to strings, as for a ConfigMap, then
// base64-encodes each value for a Secret's data field.
func secretData(merged map[string]any, formats map[string]v1beta1.ParseFormat, stringify bool) (map[string]any, []string, error) {
	lowered, coerced, err := transformer.LowerToStringMapWith(merged, formats, stringify)
	if err != nil {
		return nil, nil, err
	}
	out := make(map[string]any, len(lowered))
	for k, v := range lowered {
		s, ok := v.(string)
		if !ok {
			return nil, nil, errors.Errorf("key %q lowered to %T, want a string", k, v)
		}
		out[k] = base64.StdEncoding.EncodeToString([]byte(s))
	}
	return out, coerced, nil
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

// clusterScopedTargets are kinds this function knows are cluster scoped without
// asking the API server. A pure function has no RESTMapper, so this is a small
// explicit table, not scope inference. Match on group and kind: Kind names are
// not unique, and a user's own namespaced mygroup.io/EnvironmentConfig is
// legitimate.
var clusterScopedTargets = map[schema.GroupKind]bool{
	{Group: "apiextensions.crossplane.io", Kind: "EnvironmentConfig"}: true,
}

func isClusterScopedTarget(gk schema.GroupKind) bool {
	return clusterScopedTargets[gk]
}
