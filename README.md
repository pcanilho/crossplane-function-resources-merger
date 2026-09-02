[![CI](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/ci.yaml/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/ci.yaml)
[![Dependabot Updates](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/dependabot/dependabot-updates/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/dependabot/dependabot-updates)
[![SAST](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/sast.yaml/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/sast.yaml)

![version](https://img.shields.io/badge/Version-v0.4.1-blue)
<p align="center" width="100%">
    <img src="https://github.com/pcanilho/crossplane-function-resources-merger/blob/main/docs/images/banner.png?raw=true" width="220"></img>
    <br>
    <i><b>function-resources-merger</b></i>
    <br>
    An arbitrary resources merging function for Crossplane.
    <br>
    <br>
    ⚙️ <a href="#installing-this-function">Installing this function</a> | 🔎 <a href="#how-to-use">How-to-use</a> | 🚀 <a href="#example">Get started with an Example</a>
    <br>
    <br>
</p>

This is a crossplane function that merges Kubernetes resources data and produces a single resulting resource containing the merged result.
It supports any kind of resource.

## Requirements

* `crossplane` ≥ v2.0.0

## Installing this function

> [!NOTE]
> This function needs no RBAC. It never contacts the API server: Crossplane resolves the sources and
> applies the result under its own `ServiceAccount`. Crossplane's default `ClusterRole` covers
> `ConfigMap`s, `Secret`s and `apiextensions.crossplane.io` resources. To target an arbitrary CRD, grant
> Crossplane a `ClusterRole` labelled `rbac.crossplane.io/aggregate-to-crossplane: "true"`.
> A `DeploymentRuntimeConfig` still applies, for replicas, resource limits and node placement.

* Install using `kubectl`:

```shell
cat <<EOF | kubectl apply -f -
---
apiVersion: pkg.crossplane.io/v1
kind: Function
metadata:
  name: function-resources-merger
spec:
  package: ghcr.io/pcanilho/crossplane-function-resources-merger:v0.4.1
EOF
```

* Installing using `helm`:

```yaml
# Chart.yaml
...
dependencies:
  - name: crossplane
    version: <your-crossplane-version>
    repository: https://charts.crossplane.io/master/
---
# values.yaml
crossplane:
  function:
    packages:
      - ghcr.io/pcanilho/crossplane-function-resources-merger:v0.4.1
```

## How-to-use

### XRD scope

Define the XRD's `scope` first. It decides how the rest of this page behaves.

```yaml
apiVersion: apiextensions.crossplane.io/v2
kind: CompositeResourceDefinition
metadata:
  name: xmergers.example.canilho.net
spec:
  scope: Cluster        # or Namespaced, which is the Crossplane v2 default
  group: example.canilho.net
  names:
    kind: XMerger
    plural: xmergers
  versions:
    - name: v1alpha1
      served: true
      referenceable: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
```

|  | `scope: Cluster` | `scope: Namespaced` |
|---|---|---|
| `target.namespace` | honoured | **ignored**, pinned to the composite's namespace |
| Cross-namespace sources | unrestricted | needs `allowCrossNamespace` **and** `allowedSourceNamespaces` |
| Cluster-scoped targets such as `EnvironmentConfig` | supported | **rejected**, a namespaced composite cannot own one |

Working manifests for both scopes are in [`example/`](example/) and
[`example/namespaced/`](example/namespaced/); the worked example below walks through the
cluster-scoped one.

> [!NOTE]
> `ConfigMap` and `EnvironmentConfig` are used throughout as examples. Any resource with a
> map-shaped field works; mind the field's own type compatibility when merging into it.

### Function `Merge` specification

<details>
    <summary><i><b>debug</b> [expand]</i></summary>

`Optional`

If set to `true`, the function will output debug information.

</details>

<details>
    <summary><i><b>target</b> [expand]</i></summary>

`Mandatory`

Specifies the target resource that will be created/managed by this function.

| Field                              | Description                                                                                  |
|-------------------------------------|------------------------------------------------------------------------------------------------|
| `namespace`                         | The namespace where the target resource will be created/managed. Omit for cluster-scoped kinds. Ignored under a namespaced composite, which pins the target to its own namespace. |
| `namespaceFromCompositeFieldPath`   | (Optional) A field path on the composite resource to resolve the namespace from.                |
| `name`                              | The target resource's `metadata.name`. The `crossplane.io/composition-resource-name` annotation is derived from the resource's identity, not this value; see the worked example below.     |
| `nameFromCompositeFieldPath`        | (Optional) A field path on the composite resource to resolve the name from.                     |
| `apiVersion`                        | The API version of the target resource.                                                         |
| `kind`                              | The kind of the target resource.                                                                |
| `toFieldPath`                       | The field where the merged data is written. (defaults to `data`)                                |
| `metadata`                          | (Optional) `labels`/`annotations` to set on the target resource.                                |
| `stringifyScalars`                  | (Optional) Coerces a top-level non-string scalar (number or boolean) to a string for a `ConfigMap` or `Secret` target instead of failing. Off by default. One-way: a coerced value never returns as a number or boolean. Every coerced key is named on the `Merged` condition. |
| `readiness`                         | (Optional) `True` or `False`. Reported on the composed resource. (defaults to `True`)           |
| `context`                           | (Optional) Additionally writes the merged result into the Composition context. See below.       |

> [!TIP]
> ➤ **context** (`object`)
>
> | Field | Description                                                              |
> |-------|----------------------------------------------------------------------------|
> | `key` | The Composition context key the merged result is written to. Required when `context` is set. |
>
> This is additive: the composed resource is still created either way. It lets a later pipeline
> step read the merged result straight from `context[key]` without a second lookup of the composed
> resource.
>
> The published value is the merged result itself, whatever the target's `kind`: never stringified
> for a `ConfigMap`, never base64-encoded for a `Secret`. Those encodings exist only for the
> composed resource's own Kubernetes API shape; context is an independent destination.

</details>

> [!NOTE]
> Changing `name`, or the value `nameFromCompositeFieldPath` resolves to, drops the old target: Crossplane deletes
> the old object and creates a new one, it does not move data. Changing `apiVersion` between served versions of the
> same kind does not recreate it, since the target is identified by group and kind only.

> [!NOTE]
> `readiness` is a two-value enum, `True` or `False`, with no unset option: Crossplane collapses an
> unspecified readiness to false rather than "unknown", leaving the composite at `Ready=False,
> Reason=Creating` in a requeue loop. Set `readiness: "False"` only when another mechanism is
> responsible for flipping it true.

> [!NOTE]
> Under a namespaced composite resource, Crossplane pins the target to the composite's own
> namespace, so `target.namespace` is ignored: a warning is emitted and the ignored value is
> noted on the `Merged` condition.
> A `Secret` target whose `target.namespace` disagrees is a fatal error rather than a warning,
> since relocating secret material is not a safe thing to do quietly. A cluster-scoped target such
> as `EnvironmentConfig` cannot be composed by a namespaced composite at all.

> [!IMPORTANT]
> Under a namespaced composite, a source may only be read from the composite's own namespace, or
> from a cluster-scoped kind. Reading another namespace requires both `allowCrossNamespace` on the
> source and that namespace in `allowedSourceNamespaces`. This is defence in depth against
> mistakes: Crossplane itself does not confine reads, and they run under its own cluster-wide
> `ServiceAccount`. It is not a tenant isolation boundary, since Compositions are cluster-scoped
> and only platform operators can write them.

<details>
    <summary><i><b>sources</b> [expand]</i></summary>

`Mandatory`

A list of resources that will be used to merge into the target resource.

| Field           | Description                                                                    |
|------------------|---------------------------------------------------------------------------------|
| `name`           | A unique key identifying this source.                                           |
| `ref`            | The `apiVersion`, `kind` and `name` of the resource, plus its `namespace`. Omit `namespace` for cluster-scoped kinds such as `EnvironmentConfig`. |
| `ref.nameFromCompositeFieldPath` | (Optional) Resolves `ref.name` from a field path on the composite resource, for example `spec.tenant`. Mutually exclusive with `ref.name`; one of the two is required. The resolved value must be a DNS subdomain. Cannot be combined with `allowCrossNamespace`; see the note below. |
| `resolution`     | (Optional) `Required` or `Optional`. (defaults to `Required`)                   |
| `fromFieldPath`  | (Optional) The field to read data from. (defaults to `data`) If absent on an otherwise existing resource, the source contributes nothing rather than failing. |
| `allowCrossNamespace` | (Optional) Permits this source to be read from a namespace other than the composite's. The namespace must also appear in `allowedSourceNamespaces`. Ignored for a cluster-scoped composite. Cannot be combined with `ref.nameFromCompositeFieldPath`. |
| `toFieldPath`    | (Optional) Places this source's contribution under a dotted subtree of the merged result, for example `teams.payments`, instead of at the root. Runs before the fold, so `mergeStrategy` still applies to the nested shape. (defaults to the root) |
| `parse`          | (Optional) Scopes and shapes embedded-blob parsing for this source, overriding the Input-level `parseEmbedded`. See below. |

> [!TIP]
> ➤ **parse** (`object`)
>
> A source with no `parse` block follows the Input-level `parseEmbedded`, which is retained
> unchanged. Where both are set, the source's own `parse` wins.
>
> | Field    | Description                                                                    |
> |----------|---------------------------------------------------------------------------------|
> | `format` | (Optional) `Auto`, `YAML` or `JSON`. Forces re-encoding in that serialization, overriding the origin detected while parsing. Applies only to a `ConfigMap` or `Secret` target; any other target keeps the parsed value as a native map, and `format` has no effect. (defaults to `Auto`) |
> | `keys`   | (Optional) The data keys to parse. Empty means every key, which is what the Input-level `parseEmbedded` does. |
>
> `keys` fixes the prose hazard below: name only the keys that hold embedded config, and prose
> containing `: ` is left as a string. `format` fixes the JSON hazard on a source whose values
> hold JSON blobs, scoping with `keys` if only some do: set it to `JSON` and those values re-encode
> as JSON, not YAML.

</details>

<details>
    <summary><i><b>allowedSourceNamespaces</b> [expand]</i></summary>

`Optional`

The namespaces a source may be read from besides the composite's own, when that source also sets
`allowCrossNamespace`. Both are required: the flag says which sources may cross a namespace, the
list says which namespaces are trusted. Ignored for a cluster-scoped composite.

</details>

> [!NOTE]
> Sources are read from the API server, not from the Composition's desired state, so a resource composed by an
> earlier step of the same pipeline is not visible to this function as a source.

> [!TIP]
> Both `target` and `sources` have full support for both standard kubernetes resources and custom-resources.

### Per-tenant sources

One Composition, one overlay `ConfigMap` per tenant. `ref.nameFromCompositeFieldPath` reads the
name from the composite, so the Composition does not have to be duplicated per tenant:

```yaml
sources:
  - name: base
    ref:
      namespace: platform
      name: base-config
      apiVersion: v1
      kind: ConfigMap
  - name: overlay
    ref:
      namespace: platform
      nameFromCompositeFieldPath: spec.tenant
      apiVersion: v1
      kind: ConfigMap
```

> [!NOTE]
> This example assumes a cluster-scoped composite. Both sources pin `namespace: platform`
> statically; under a namespaced composite outside `platform` that is a cross-namespace read, and
> `allowCrossNamespace` cannot rescue it, since it is rejected alongside
> `ref.nameFromCompositeFieldPath`. A namespaced composite needs its overlay sources pinned to its
> own namespace instead.

A composite with `spec.tenant: acme` merges `platform/base-config` then `platform/acme`. The
`Merged` condition records every resolution, so the resource actually read is visible on the
composite: `2 of 2 sources merged; resolved names: overlay -> acme`.

> [!IMPORTANT]
> Whoever can write the composite chooses the resolved **name**, never the namespace, and
> Crossplane fetches sources under its own cluster-wide ServiceAccount, which no per-name RBAC
> restricts. Two rules bound that: a source has no `namespaceFromCompositeFieldPath`, so the
> namespace stays a static string the Composition author pinned; and
> `ref.nameFromCompositeFieldPath` cannot be combined with `allowCrossNamespace`, which together
> would let a composite name any resource of that kind inside a permitted foreign namespace.
>
> What remains: under a namespaced composite a resolved name can be steered at any resource of its
> kind **in the composite's own namespace**, including one the composite's owner could not read
> directly if their RBAC excludes it by `resourceNames`. Keep such sources to kinds whose whole
> namespace that principal may already read. A name that is missing, empty, or not a DNS subdomain
> omits that source's selector entirely and fails the composition.

### Placing a source under a subtree

Two sources merging into one `EnvironmentConfig`, one at the root and one nested under
`teams.payments` via `toFieldPath`:

```yaml
sources:
  - name: shared
    ref:
      namespace: platform
      name: shared-config
      apiVersion: v1
      kind: ConfigMap
  - name: payments
    toFieldPath: teams.payments
    ref:
      namespace: platform
      name: payments-config
      apiVersion: v1
      kind: ConfigMap
```

```yaml
apiVersion: apiextensions.crossplane.io/v1beta1
kind: EnvironmentConfig
metadata:
  name: merged
data:
  shared: "yes"
  teams:
    payments:
      owner: payments
```

> [!NOTE]
> A `ConfigMap` or `Secret` target's `data` is `map[string]string`, so a nested subtree is not
> representable. It is YAML-encoded into a single string value at the top-level key of the path,
> so expect one encoded blob per top-level prefix rather than nested keys.
>
> When a source combines `toFieldPath` with `parse`, the nested subtree collapses to one blob:
> `parse.format: Auto` always re-encodes it as YAML, since per-key origin detection no longer
> applies once the keys are nested away. Set `parse.format` explicitly to override this.

### Secrets

A `Secret` source is base64-decoded on read, so the merge operates on plaintext exactly as it
does for a `ConfigMap`. A `Secret` target has every merged value base64-encoded back into
`data`.

- **A `Secret` source requires a `Secret` target.** Anything else is a fatal error. Merging
  secret material into a `ConfigMap` would write it out in plaintext.
- **`stringData` is never written**, only `data`. Server-side apply never owns `stringData`, so
  a key removed from the merge would never be removed from the `Secret`.
- Under a namespaced composite, a `Secret` target whose `target.namespace` disagrees with the
  composite's namespace is fatal rather than a warning. Relocating secret material quietly is
  not safe.

### `mergeStrategy`

Selects how each source folds into the accumulator. The default, `ForceMergeObjects`, has later
sources win, which is what layering overrides on a base needs. `MergeObjects` and
`MergeObjectsAppendArrays` are fill-only, so an **earlier** source takes precedence instead.

| Strategy | Conflicting scalar | Nested map | Arrays | Explicit null from a later source |
|---|---|---|---|---|
| `ForceMergeObjects` (default) | later source wins | recursive, later wins | replaced | kept |
| `ForceMergeObjectsAppendArrays` | later source wins | recursive, later wins | appended | kept |
| `MergeObjects` | **earlier source wins** | recursive, fill-only | kept | **dropped** |
| `MergeObjectsAppendArrays` | **earlier source wins** | recursive, fill-only | appended | **dropped** |
| `Replace` | later source wins | **replaced wholesale** | replaced | kept |

### `parseEmbedded`

`true` parses string values that are YAML mappings so their contents deep-merge; `false`, the
default, merges them as-is. Scope or override it per source with `sources[].parse`.

> [!IMPORTANT]
> Parsing round-trips a value through a YAML decoder and encoder, which preserves the data but
> not its formatting. With `parseEmbedded: true`:
>
> - **A JSON value is re-encoded as YAML.** JSON is valid YAML, so `{"name":"svc","port":8080}`
>   comes back as `name: svc\nport: 8080\n`. Set that source's `parse.format` to `JSON` to keep it
>   as JSON.
> - **Prose containing `: ` is parsed as a mapping.** `error: connection refused` becomes a map,
>   not a string. Scope that source's `parse.keys` to the keys that actually hold embedded config
>   to avoid this.
> - **Scalars inside a parsed blob gain quotes**: `a: no` re-encodes as `a: "no"`. It stays the
>   string `no`; yaml.v3 is YAML 1.2 and does not coerce it to a boolean.
> - **Trailing zeros are lost**: `3.10` re-encodes as `3.1`.
> - **Key order, indentation, comments and anchors are not preserved.** A round-tripped blob
>   comes back alphabetically sorted and 2-space indented even when nothing merged into it.
>
> A top-level value that is not a mapping is never parsed and passes through untouched. A blob
> containing more than one YAML document with real content is left unparsed and merged as opaque
> text. A trailing `---` with nothing after it but whitespace, comments, or an explicit `null`
> carries no content, so it does not count as a second document and the blob is still parsed.

## Example (`local`)

> [!IMPORTANT]
> **Goal**: Merge 2x`ConfigMap`s and 2x`EnvironmentConfig`s into a single resulting `ConfigMap`.

* `ConfigMap` (map-1):
  ```yaml
  data:
    key1: a
    key2: b
  ```
* `ConfigMap` (map-2):
  ```yaml
  data:
    key2: c
    key4: d
  ```
* `EnvironmentConfig` (envcfg-1):
  ```yaml
  data:
    key1: e
    key6: f
   ```
* `EnvironmentConfig` (envcfg-2):
   ```yaml
  data:
    key4: g
    key5: h
   ```

---

<details> 
    <summary><i>Composition ⚙️</i></summary>

```yaml
---
apiVersion: apiextensions.crossplane.io/v1
kind: Composition
metadata:
  name: function-resources-merger
spec:
  compositeTypeRef:
    apiVersion: example.canilho.net/v1alpha1
    kind: XMerger
  mode: Pipeline
  pipeline:
    - step: run
      functionRef:
        name: function-resources-merger
      input:
        apiVersion: merger.fn.canilho.net/v1beta1
        kind: Merge
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
              namespace: ephemeral
              name: map-1
              apiVersion: v1
              kind: ConfigMap
          - name: map-2
            ref:
              namespace: ephemeral
              name: map-2
              apiVersion: v1
              kind: ConfigMap
          - name: envcfg-1
            ref:
              name: envcfg-1
              apiVersion: apiextensions.crossplane.io/v1beta1
              kind: EnvironmentConfig
          - name: envcfg-2
            ref:
              name: envcfg-2
              apiVersion: apiextensions.crossplane.io/v1beta1
              kind: EnvironmentConfig
```

</details>

<details> 
    <summary><i>XR ⚙️</i></summary>

```yaml
---
apiVersion: example.canilho.net/v1alpha1
kind: XMerger
metadata:
  name: merger-results-xr
spec:
  appName: merged
```

</details>

The `Function` manifest for local rendering is in [`example/functions.yaml`](example/functions.yaml).

---

* The resulting `ConfigMap` (merged):
  ```yaml
  ---
  apiVersion: v1
  kind: ConfigMap
  data:
    key1: e
    key2: c
    key4: g
    key5: h
    key6: f
  metadata:
    name: merged
    namespace: ephemeral
    annotations:
      crossplane.io/composition-resource-name: configmap/ephemeral/merged
    labels:
      crossplane.io/composite: merger-results-xr
    ownerReferences:
      - apiVersion: example.canilho.net/v1alpha1
        kind: XMerger
        name: merger-results-xr
        controller: true
        blockOwnerDeletion: true
        uid: <xr-uid>
  ...
  ```

### Test it out!

1. Launch the function locally in a separate shell:
    ```shell
      go run . --insecure --debug
    ```
2. Render the cluster scoped example:
    ```shell
    cd example && crossplane composition render xr.yaml composition.yaml functions.yaml \
      --xrd xrd.yaml \
      --required-resources required-resources.yaml \
      --crossplane-image xpkg.crossplane.io/crossplane/crossplane:v2.4.0
    ```
3. Or the namespaced example:
    ```shell
    cd example/namespaced && crossplane composition render xr.yaml composition.yaml functions.yaml \
      --xrd xrd.yaml \
      --required-resources required-resources.yaml \
      --crossplane-image xpkg.crossplane.io/crossplane/crossplane:v2.4.0
    ```
