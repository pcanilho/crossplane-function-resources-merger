# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.1] - 2026-09-02

Maintenance only. The merge behaviour, the `Merge` input schema and the `Merged`
condition are identical to v0.4.0, so a consumer on v0.4.0 gains nothing
functionally by upgrading. It exists so consumers can pick up the refreshed
dependency tree, since this package deliberately publishes no moving tag.

### Changed

- Dependencies bumped: `crossplane-runtime/v2` 2.3.1 to 2.4.0,
  `alecthomas/kong` 0.9.0 to 1.16.1, `google.golang.org/protobuf` 1.36.11 to
  1.36.12, `k8s.io/apimachinery` 0.35.3 to 0.36.4 and
  `sigs.k8s.io/controller-tools` 0.20.1 to 0.21.0. The generated input CRD is
  unchanged apart from its `controller-gen.kubebuilder.io/version` annotation.
- `k8s.io/apimachinery` stays on the v0.36 line rather than v0.37.0.
  `crossplane-runtime` v2.4.0 pins `controller-runtime` v0.23.1, which holds
  `k8s.io/api` at v0.36, and `k8s.io/api@v0.36` calls `validate.EachSliceVal`,
  which apimachinery removed in v0.37. The pairing does not compile.
- CI and the Dockerfile build with Go 1.27.1, up from 1.27.0. The `go.mod` floor
  stays at 1.27.0: the SAST job runs gosec's pinned 2.29.0 image, which is Go
  1.27.0 with `GOTOOLCHAIN=local` and cannot build a `go 1.27.1` module.
- `go fix` rewrites in `fn.go`, both mechanical and neither altering behaviour:
  a key copy loop is now `maps.Copy`, and a reverse index loop is now
  `slices.Backward`.

## [0.4.0] - 2026-09-01

### Changed

- **Breaking:** a composed resource an earlier pipeline step produced under the same derived
  key is no longer silently overwritten. It is now a fatal error. If you relied on an earlier
  step creating the shell and this function filling `data`, that pipeline must be restructured.
- **The `Input` API moved from `resources-merger.fn.canilho.net/v1alpha2` to
  `merger.fn.canilho.net/v1beta1`, and the kind is now `Merge`.** `Input` was
  the placeholder name left over from `crossplane/function-template-go`, and
  `resources-` narrowed nothing: everything in a Composition is resources.
  Update the `input:` block of every pipeline step that references this
  function:

  ```yaml
  # before
  apiVersion: resources-merger.fn.canilho.net/v1alpha2
  kind: Input

  # after
  apiVersion: merger.fn.canilho.net/v1beta1
  kind: Merge
  ```

  No field names changed, so the rest of each `input:` block is untouched.
  Nothing in the cluster needs recreating: the input is opaque bytes embedded
  in a Composition, never a stored object in its own right.
- The `Merged` condition reports `False` with reason `NoData` when no source contributed data.
  Previously it was always `True`/`Success`, so a typo'd `fromFieldPath` that emptied the merge
  reported success.
- A resolved `target.nameFromCompositeFieldPath` value must be a DNS subdomain. A field path
  that resolves to something else is now fatal, rather than composing a resource whose name the
  API server would reject anyway, or corrupting the desired-state key's separators.

### Added

- `sources[].toFieldPath` places one source's contribution under a subtree of the merged result
  instead of at the root.
- `sources[].parse.format` and `sources[].parse.keys` scope and shape embedded-blob parsing per
  source, overriding the Input-level `parseEmbedded`. `format` (`Auto`, `YAML` or `JSON`)
  controls re-encoding on output for a `ConfigMap` or `Secret` target; `keys` scopes which data
  keys are parsed.
- `sources[].ref.nameFromCompositeFieldPath` resolves a source's name from a field path on the
  composite, for a per-tenant overlay chosen by the XR. The namespace stays a static string and
  cannot be combined with `allowCrossNamespace`.
- `target.stringifyScalars` coerces a top-level non-string scalar to a string for a `ConfigMap`
  or `Secret` target instead of failing. Off by default; one-way.
- `target.readiness` (`True` or `False`) lets a Composition author override the resource's
  reported readiness. Defaults to `True`.
- `target.context.key` additionally writes the merged result into the Composition context, for
  a later pipeline step to read without a second lookup of the composed resource. The composed
  resource is still produced.

### Fixed

- Requirement keys are namespaced under this function's own API group, so a Composition's own
  `requirementName` can no longer collide with a source name and shadow it.
- The missing-required-source error now names the namespace, the field most often got wrong.
- Every configuration problem is reported in one `Fatal`, not just the first. Three
  misconfigured sources used to take three reconciles to fix, one at a time.
- A source's `parse.format`, detected from its own JSON or YAML origin, no longer leaks onto a
  same-named top-level key contributed by a different, nested source.
- `target.context`'s value no longer inherits `ConfigMap` stringification or `Secret`
  base64-encoding: it is written from the merged result before that coercion runs, so it carries
  the natural typed value regardless of the target's kind.

## [0.3.1] - 2026-09-01

Maintenance only. The merge behaviour, the `Input` schema and the `Merged`
condition are identical to v0.3.0, so a consumer on v0.3.0 gains nothing by
upgrading.

### Changed

- The `go.mod` Go version is now 1.27.0, up from 1.25.10. CI and the Dockerfile
  already built with 1.27.0; only the floor moved.
- `go fix` rewrites, both mechanical and neither altering behaviour: two
  hand-rolled search loops in `fn.go` are now `slices.Contains`, and the `ptr`
  test helper was inlined to Go 1.26's `new(expr)`.
- The SAST workflow pins `securego/gosec` to a commit past the v2.29.0 tag.
  That tag's `action.yml` still points at the 2.28.0 image, which ships Go
  1.26.5 with `GOTOOLCHAIN=local` and so cannot build a `go 1.27.0` module.

## [0.3.0] - 2026-08-29

Namespaced composite resources are now supported. This is the default shape in
Crossplane v2, so the function was previously unusable for most v2 consumers.

Cluster-scoped consumers are unaffected by that work, but should read the first
entry under Changed: a source whose field is absent no longer fails the
composition.

### Added

- `sources[].allowCrossNamespace` and `allowedSourceNamespaces`, which together
  permit a source to be read from a namespace other than the composite's own.
  Both are required. Ignored for a cluster-scoped composite.

### Changed

- A source resource that exists but has no field at `fromFieldPath` no longer
  fails the composition. It contributes nothing instead, under either
  `resolution`. This affects every consumer, not only namespaced ones: a
  Composition that fails today with `has no field` will start succeeding, with
  one fewer source contributing. A field present but not an object, or any
  other malformed `fromFieldPath`, is still fatal; only a genuinely absent
  field is affected. `kubectl create configmap foo` produces exactly this
  shape, so an empty `ConfigMap` can now be merged.
- The `Merged` condition names an absent field under its own `no data: <name>`
  clause, distinct from `skipped optional`, so a resource that was never there
  and a resource that was there but empty stay tellable apart.
- A namespaced composite is no longer rejected. Under one, `target.namespace` is
  ignored, because Crossplane pins a composed resource to the composite's own
  namespace regardless of what a function asks for. A warning is emitted and
  the ignored value is noted on the `Merged` condition.
- Under a namespaced composite, a source may only be read from the composite's
  own namespace or from a cluster-scoped kind, unless both new fields permit it.
  This is stricter than Crossplane, which does not confine reads at all.

### Fixed

- A `Secret` target whose `target.namespace` disagrees with a namespaced
  composite is now a fatal error rather than a silent relocation of secret
  material into the namespace the composite controls.
- A cluster-scoped target such as `EnvironmentConfig` under a namespaced
  composite now fails immediately, naming the target, instead of being created
  and then orphaned. A cluster-scoped object owned by a namespaced composite can
  never be garbage collected.

## [0.2.0] - 2026-08-29

A full rewrite of the function as a pure composition function on Crossplane v2.
Every `Input` field and every XR field from v0.1.x changed. Read
[Migrating from v0.1.x](#migrating-from-v01x) before upgrading.

### Breaking

- **Crossplane v2.0.0 is now the minimum.** The function resolves its sources
  through `req.RequiredResources`, which Crossplane only began populating in
  v2.0.0. `package/crossplane.yaml` declares `spec.crossplane.version:
  ">=v2.0.0"`, so older Crossplane refuses to install the package.
- **The package is renamed** from `function-xresources-merger` to
  `function-resources-merger`, and its metadata moved from
  `meta.pkg.crossplane.io/v1beta1` to `meta.pkg.crossplane.io/v1`. Update the
  `Function` and every `functionRef.name` that points at it.
- **The `Input` API moved from `v1alpha1` to `v1alpha2`** with an incompatible
  shape. `targetRef` became `target`, `sourceRefs` became `sources`, and the
  inline `TypedReference` on each source became a `ref` block. Each source now
  carries a required unique `name`, which is the key Crossplane resolves it
  under. A source's `key` and `extractFromKey` collapsed into one
  `fromFieldPath`, a full field path into the resource such as `data.settings`;
  the target's `key` became `toFieldPath`. Both default to `data`.
- **Merge behaviour is configured on the `Input`, not on the XR.** The XR's
  `spec.options` map (`override`, `appendSlice`, `sliceDeepCopy`,
  `overwriteEmptyValue`, `overrideEmptySlice`, `typeCheck`) and
  `spec.transform.stringToMap` are gone, replaced by the `mergeStrategy` enum
  and `parseEmbedded`. `spec.debug` moved to the `Input`'s `debug` field.
- **`spec.mode` is gone.** The function no longer writes `ownerReferences` by
  hand. The merged resource is a composed resource, so Crossplane reconciles it
  and garbage collects it with the XR. There is no unmanaged mode.
- **The function no longer calls the Kubernetes API.** It has no client, no
  kubeconfig and no direct reads. Sources are resolved by Crossplane through
  the required resources handshake, so they must be readable by Crossplane's
  own `ServiceAccount`.
- **The composite resource must be cluster scoped.** Any namespaced XR is
  rejected with a fatal result, unconditionally. Namespaced is the default
  shape for an `apiextensions.crossplane.io/v2` XRD, so the XRD must say
  `scope: Cluster` explicitly. Namespaced support is deferred.
- A `Secret` source requires a `Secret` target. Merging a `Secret` into a
  `ConfigMap` is rejected rather than emitting base64 into plaintext.
- The example XRD moved to `apiextensions.crossplane.io/v2` with an explicit
  `scope: Cluster`, dropped `claimNames`, and renamed the composite kind from
  `xMerger` to `XMerger`.

### Added

- `mergeStrategy` with five named strategies matching the vocabulary of
  `function-patch-and-transform`: `Replace`, `MergeObjects`,
  `ForceMergeObjects`, `MergeObjectsAppendArrays` and
  `ForceMergeObjectsAppendArrays`. The default is `ForceMergeObjects`.
- `resolution: Required | Optional` per source. An `Optional` source that does
  not exist is skipped and named in the `Merged` condition; a `Required` one
  that does not exist is fatal.
- `target.nameFromCompositeFieldPath` and `target.namespaceFromCompositeFieldPath`
  to derive the target's identity from the XR.
- `target.metadata.labels` and `target.metadata.annotations`.
- `parseEmbedded`, which decodes string values that are YAML mappings so an
  embedded document deep merges instead of being replaced wholesale.
- `Secret` support. Values read from a `Secret` source are base64 decoded
  before the merge and re-encoded into `data` for a `Secret` target.
  `stringData` is never written, because server side apply does not persist it.
- A `Merged` condition on the composite and claim reporting how many sources
  merged and which optional ones were skipped.
- The composed resource is emitted with `Ready: True`, so it does not hold the
  composite unready.
- Unit tests for the merger and transformer packages, and an offline
  `crossplane render` golden file check in CI, driven by
  `example/required-resources.yaml` and `example/rendered.golden.yaml`.
- `.ko.yaml`, so the runtime image is built with `ko` instead of Docker buildx.

### Changed

- Sources merge in declared order. Under `ForceMergeObjects`,
  `ForceMergeObjectsAppendArrays` and `Replace` a later source wins a conflict;
  under `MergeObjects` and `MergeObjectsAppendArrays` the first value set wins.
- The merge no longer mutates its inputs. Both sides are deep copied.
- Go 1.22.3 to 1.25.10 in `go.mod`; CI builds and tests on Go 1.27.
- `function-sdk-go` v0.2.0 to v0.7.1, `crossplane-runtime` v1.15.0 to
  `crossplane-runtime/v2` v2.3.1, `k8s.io/apimachinery` v0.30.3 to v0.35.3,
  `mergo` v1.0.0 to v1.0.2.

### Removed

- `k8s.io/client-go` and the `internal/k8s` controller that wrapped it.
- `internal/maps`.
- The dead code block in `fn.go` that worked around
  [provider-ansible#172](https://github.com/crossplane-contrib/provider-ansible/issues/172).
  That issue concerned namespaced composed resources under Crossplane v1 and
  was fixed before v1.15.

### Fixed

- The merged resource is now part of the desired state, so it is reconciled on
  drift and deleted with the XR. Previously it was created out of band with a
  direct API call and orphaned on deletion.
- Earlier pipeline steps' desired resources are preserved instead of being
  overwritten.
- Sources are walked in declared order rather than by ranging a Go map, so the
  merge result no longer depends on map iteration order.

### Migrating from v0.1.x

Before:

```yaml
# XR
spec:
  mode: managed
  debug: true
  options:
    override: true
  transform:
    stringToMap: true

# Composition input
apiVersion: resources-merger.fn.canilho.net/v1alpha1
kind: Input
targetRef:
  namespace: ephemeral
  name: merged
  apiVersion: v1
  kind: ConfigMap
sourceRefs:
  - namespace: ephemeral
    name: map-1
    apiVersion: v1
    kind: ConfigMap
```

After:

```yaml
# XR
spec:
  appName: merged

# Composition input
apiVersion: resources-merger.fn.canilho.net/v1alpha2
kind: Input
debug: true
mergeStrategy: ForceMergeObjects
parseEmbedded: true
target:
  apiVersion: v1
  kind: ConfigMap
  nameFromCompositeFieldPath: spec.appName
  namespace: ephemeral
sources:
  - name: map-1
    ref:
      apiVersion: v1
      kind: ConfigMap
      namespace: ephemeral
      name: map-1
```

`options.override: true` maps to `mergeStrategy: ForceMergeObjects`, and
`options.override` unset maps to `MergeObjects`. Add `options.appendSlice` to
either to reach the `AppendArrays` variant. `transform.stringToMap` maps to
`parseEmbedded`. A source's `key: spec` plus `extractFromKey: settings` becomes
`fromFieldPath: spec.settings`. `sliceDeepCopy`, `overwriteEmptyValue`,
`overrideEmptySlice` and `typeCheck` have no equivalent and were dropped.

Resources merged by v0.1.x are not adopted. They were created directly rather
than composed, so Crossplane does not know about them. Delete them before
upgrading, or the function's server side apply will contend with whatever is
already there.

## [0.1.8] - 2024-08-12

See the [release notes](https://github.com/pcanilho/crossplane-function-resources-merger/releases/tag/v0.1.8).

[0.4.1]: https://github.com/pcanilho/crossplane-function-resources-merger/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/pcanilho/crossplane-function-resources-merger/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/pcanilho/crossplane-function-resources-merger/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/pcanilho/crossplane-function-resources-merger/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/pcanilho/crossplane-function-resources-merger/compare/v0.1.8...v0.2.0
[0.1.8]: https://github.com/pcanilho/crossplane-function-resources-merger/releases/tag/v0.1.8
