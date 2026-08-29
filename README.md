[![CI](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/ci.yaml/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/ci.yaml)
[![Dependabot Updates](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/dependabot/dependabot-updates/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/dependabot/dependabot-updates)
[![SAST](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/sast.yaml/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/sast.yaml)

![version](https://img.shields.io/badge/Version-v0.2.0-blue)
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
> This function reads and writes resources using Crossplane's own `ServiceAccount`, not a dedicated
> `DeploymentRuntimeConfig`. Its default `ClusterRole` covers `ConfigMap`s, `Secret`s and
> `apiextensions.crossplane.io` resources. To target an arbitrary CRD, grant it a `ClusterRole` labelled
> `rbac.crossplane.io/aggregate-to-crossplane: "true"`.

* Install using `kubectl`:

```shell
cat <<EOF | kubectl apply -f -
---
apiVersion: pkg.crossplane.io/v1
kind: Function
metadata:
  name: function-resources-merger
spec:
  package: ghcr.io/pcanilho/crossplane-function-resources-merger:v0.2.0
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
      - ghcr.io/pcanilho/crossplane-function-resources-merger:v0.2.0
```

The above Helm chart will install the `pcanilho-crossplane-function-resources-merger` function into the Crossplane
runtime.

## How-to-use

### Function `Input` specification

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
| `namespace`                         | The namespace where the target resource will be created/managed. Omit for cluster-scoped kinds. |
| `namespaceFromCompositeFieldPath`   | (Optional) A field path on the composite resource to resolve the namespace from.                |
| `name`                              | The target resource's `metadata.name`. The `crossplane.io/composition-resource-name` annotation is derived from the resource's identity, not this value; see the worked example below.     |
| `nameFromCompositeFieldPath`        | (Optional) A field path on the composite resource to resolve the name from.                     |
| `apiVersion`                        | The API version of the target resource.                                                         |
| `kind`                              | The kind of the target resource.                                                                |
| `toFieldPath`                       | The field where the merged data is written. (defaults to `data`)                                |
| `metadata`                          | (Optional) `labels`/`annotations` to set on the target resource.                                |

</details>

> [!NOTE]
> Changing `name`, or the value `nameFromCompositeFieldPath` resolves to, drops the old target: Crossplane deletes
> the old object and creates a new one, it does not move data. Changing `apiVersion` between served versions of the
> same kind does not recreate it, since the target is identified by group and kind only.

> [!NOTE]
> A namespaced consumer XR is unsupported: the function returns a fatal error naming
> `target.namespace`, since the target requires a cluster-scoped composite resource.

<details>
    <summary><i><b>sources</b> [expand]</i></summary>

`Mandatory`

A list of resources that will be used to merge into the target resource.

| Field           | Description                                                                    |
|------------------|---------------------------------------------------------------------------------|
| `name`           | A unique key identifying this source.                                           |
| `ref`            | The `apiVersion`, `kind` and `name` of the resource, plus its `namespace`. Omit `namespace` for cluster-scoped kinds such as `EnvironmentConfig`. |
| `resolution`     | (Optional) `Required` or `Optional`. (defaults to `Required`)                   |
| `fromFieldPath`  | (Optional) The field to read data from. (defaults to `data`)                    |

</details>

> [!NOTE]
> Sources are read from the API server, not from the Composition's desired state, so a resource composed by an
> earlier step of the same pipeline is not visible to this function as a source.

> [!TIP]
> Both `target` and `sources` have full support for both standard kubernetes resources and custom-resources.

### Specification

1. Select which is the target resource that will be created/managed by this function.
    * Examples: `ConfigMap`, `EnvironmentConfig`, etc.
    * Using a `ConfigMap`:
       ```yaml
       target:
         namespace: <target-namespace>
         name: <target-name>
         apiVersion: v1
         kind: ConfigMap
       ```
2. Identify which resources will be merged into the target resource.
    * Two `ConfigMap`s and a `EnvironmentConfig`:
       ```yaml
       sources:
         - name: <source-name>
           ref:
             namespace: <resource-namespace>
             name: <resource-name>
             apiVersion: v1
             kind: ConfigMap
         - name: <source-name>
           ref:
             namespace: <resource-namespace>
             name: <resource-name>
             apiVersion: v1
             kind: ConfigMap
         - name: <source-name>
           ref:
             name: <resource-name>
             apiVersion: apiextensions.crossplane.io/v1beta1
             kind: EnvironmentConfig
       ```
3. Define what merge strategy should be used through the `Input`'s `mergeStrategy` field.
    * Example:
       ```yaml
       mergeStrategy: ForceMergeObjects
       parseEmbedded: true
       ```
4. Observe the merged resource.
    * Example:
       ```yaml
       apiVersion: v1
       kind: ConfigMap
       metadata:
         name: <target-name>
         namespace: <target-namespace>
       data:
         foo: bar
       ```

> [!NOTE]
> `ConfigMap` and `EnvironmentConfig` resources are used as an example. This function can be used with any Kubernetes
> resource that contains a `data` field in its spec.
> Do note that the data-type compatibility of `data` spec field should to be taken into account when merging results.

> [!TIP]
> The `Input`'s `mergeStrategy` field selects how each source merges into the accumulator. Later sources win by
> default; the previous, XR-based default was the opposite (first source wins) and was undocumented.
>
> ➤ **mergeStrategy** (`string`)
> | Value | Description |
> | --- | --- |
> | `Replace` | Top-level keys from the later source replace the accumulator wholesale. |
> | `MergeObjects` | Deep merge; existing non-empty values win over later sources. |
> | `ForceMergeObjects` | Deep merge; later sources overwrite existing values. (`default`) |
> | `MergeObjectsAppendArrays` | `MergeObjects`, and arrays are appended instead of overwritten. |
> | `ForceMergeObjectsAppendArrays` | `ForceMergeObjects`, and arrays are appended instead of overwritten. |
>
> ➤ **parseEmbedded** (`boolean`)
> | Value | Description |
> | --- | --- |
> | `true` | String values that are YAML mappings are parsed so their contents deep-merge. |
> | `false` | String values are merged as-is. (`default`) |
>
> Parsing round-trips a blob through YAML: key order, comments and indentation are not preserved, and YAML 1.1
> coercion applies inside it, so an unquoted `no` becomes `false`. A blob containing more than one YAML document with
> real content is left unparsed and merged as opaque text. A trailing `---` with nothing after it but whitespace,
> comments, or an explicit `null` carries no content, so it does not count as a second document and the blob is
> still parsed.
>
> ➤ **debug** (`boolean`)
> | Value | Description |
> | --- | --- |
> | `true` | The function will output debug information. |
> | `false` | The function will not output debug information. (`default`) |

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
    apiVersion: resources-merger.fn.canilho.net/v1alpha1
    kind: XMerger
  mode: Pipeline
  pipeline:
    - step: run
      functionRef:
        name: function-resources-merger
      input:
        apiVersion: resources-merger.fn.canilho.net/v1alpha2
        kind: Input
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
apiVersion: resources-merger.fn.canilho.net/v1alpha1
kind: XMerger
metadata:
  name: merger-results-xr
spec:
  appName: merged
```

</details>

<details> 
    <summary><i>Function ⚙️</i></summary>

```yaml
---
apiVersion: pkg.crossplane.io/v1
kind: Function
metadata:
  name: function-resources-merger
  annotations:
    # This tells crossplane composition render to connect to the function locally.
    render.crossplane.io/runtime: Development
spec:
  # This is ignored when using the Development runtime.
  package: function-resources-merger

```

</details>

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
      - apiVersion: resources-merger.fn.canilho.net/v1alpha1
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
2. Render the example:
    ```shell
    cd example && crossplane composition render xr.yaml composition.yaml functions.yaml \
      --xrd xrd.yaml \
      --required-resources required-resources.yaml \
      --crossplane-image xpkg.crossplane.io/crossplane/crossplane:v2.3.4
    ```

##### References

* `functions`: https://docs.crossplane.io/latest/concepts/composition-functions
* `go`: https://go.dev
* `ko`: https://ko.build
* `docker`: https://www.docker.com
* `cli`: https://docs.crossplane.io/latest/cli
