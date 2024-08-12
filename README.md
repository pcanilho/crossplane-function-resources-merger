[![CI](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/ci.yaml/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/ci.yaml)
[![Dependabot Updates](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/dependabot/dependabot-updates/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/dependabot/dependabot-updates)
[![SAST](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/sast.yaml/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/sast.yaml)

![version](https://img.shields.io/badge/Version-v0.1.7-blue)
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

* `crossplane` ≥ v1.15 (recommended)

## Installing this function

> [!IMPORTANT]
> It is recommended that this function is created with a custom `DeploymentRuntimeConfig` as to allow
> for the function to have the necessary permissions to get/update/create resources.

* Install using `kubectl`:

```shell
cat <<EOF | kubectl apply -f -
---
apiVersion: pkg.crossplane.io/v1beta1
kind: Function
metadata:
  name: function-xresources-merger
spec:
  package: ghcr.io/pcanilho/crossplane-function-resources-merger:v0.1.7
EOF
```

> [!TIP]
> If different permissions are to be granted to the function, a `(Cluster)Role` and `(Cluster)RoleBinding` should be
> created and
> attached to the `ServiceAccount` managed by a `DeploymentRuntimeConfig`. Once you're ready, add the below block to the
> above document:
>
> ```yaml
> ...
> spec:
>   ...
>   runtimeConfigRef:
>     apiVersion: pkg.crossplane.io/v1beta1
>     kind: RuntimeConfig
>     name: <your-DeploymentRuntimeConfig>
> ```

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
      - ghcr.io/pcanilho/crossplane-function-resources-merger:v0.1.7
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
    <summary><i><b>targetRef</b> [expand]</i></summary>

`Mandatory`

Specifies the target resource that will be created/managed by this function.

| Field        | Description                                                                                 |
|--------------|---------------------------------------------------------------------------------------------|
| `namespace`  | The namespace where the target resource will be created/managed.                            |
| `name`       | The name of the target composition resource name `crossplane.io/composition-resource-name`. |
| `apiVersion` | The API version of the target resource.                                                     |
| `kind`       | The kind of the target resource.                                                            |
| `key`        | The key to the root object field holding data. (defaults to `data`)                         |

</details>

<details>
    <summary><i><b>sourceRefs</b> [expand]</i></summary>

`Mandatory`

A list of resources that will be used to merge into the target resource.

| Field            | Description                                                         |
|------------------|---------------------------------------------------------------------|
| `namespace`      | The namespace where the resource is located.                        |
| `name`           | The name of the resource.                                           |
| `apiVersion`     | The API version of the resource.                                    |
| `kind`           | The kind of the resource.                                           |
| `key`            | The key to the root object field holding data. (defaults to `data`) |
| `extractFromKey` | (Optional) The key to extract the data from the resource.           |

</details>

> [!TIP]
> Both `targetRef` and `sourceRefs` have full support for both standard kubernetes resources and custom-resources.

### Specification

1. Select which is the target resource that will be created/managed by this function.
    * Examples: `ConfigMap`, `EnvironmentConfig`, etc.
    * Using a `ConfigMap`:
       ```yaml
       targetRef:
         namespace: <target-namespace>
         name: <target-name>
         apiVersion: v1
         kind: ConfigMap
       ```
2. Identify which resources will be merged into the target resource.
    * Two `ConfigMap`s and a `EnvironmentConfig`:
       ```yaml
       resources:
         - namespace: <resource-namespace>
           name: <resource-name>
           apiVersion: v1
           kind: ConfigMap
         - namespace: <resource-namespace>
           name: <resource-name>
           apiVersion: v1
           kind: ConfigMap
         - namespace: <resource-namespace>
           name: <resource-name>
           apiVersion: apiextensions.crossplane.io/v1alpha1
           kind: EnvironmentConfig
       ```
3. Define what merging options should be used through the `XR` resource.
    * Example:
       ```yaml
       options:
         override: true
         appendSlice: true
         sliceDeepCopy: true
       transform:
         stringToMap: true
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
> The `XR` can be leveraged to define the merging `boolean` options.
>
> ➤ **options** (`map`)
> | Option | Type | Description |
> | --- | --- | --- |
> | `override` | `boolean` | Merge override non-empty dst attributes with non-empty src attributes values. |
> | `typeCheck` | `boolean` | Merge check types while overwriting it (must be used with `override`). |
> | `appendSlice` | `boolean` | Merge append slices instead of overwriting it. |
> | `sliceDeepCopy` | `boolean` | Merge slice element one by one with Overwrite flag. |
> | `overwriteEmptyValue` | `boolean` | Merge override non-empty dst attributes with empty src attributes values. |
> | `overrideEmptySlice` | `boolean` | Merge override empty dst slice with empty src slice. |
>
> ➤ **transform** (`map`)
> | Option | Type | Description |
> | --- | --- | --- |
> | `stringToMap` | `boolean` | String values will be transformed to maps when possible. Allowing for deep-merging. |
>
> ➤ **mode** (`string`)
> | Option | Description |
> | --- | --- |
> | `managed` | The function will create a managed resource. (`default`)|
> | `unmanaged` | The function will create an unmanaged resource. (deletion is not finalized by crossplane) |
> 
> ➤ **debug** (`boolean`)
> | Option | Description |
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
  name: function-xresources-merger
spec:
  compositeTypeRef:
    apiVersion: resource-merger.canilho.net/v1alpha1
    kind: XR
  mode: Pipeline
  pipeline:
    - step: run
      functionRef:
        name: function-xresources-merger
      input:
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
          - namespace: ephemeral
            name: map-2
            apiVersion: v1
            kind: ConfigMap
          - namespace: ephemeral
            name: envcfg-1
            apiVersion: apiextensions.crossplane.io/v1alpha1
            kind: EnvironmentConfig
          - namespace: ephemeral
            name: envcfg-2
            apiVersion: apiextensions.crossplane.io/v1alpha1
            kind: EnvironmentConfig
```

</details>

<details> 
    <summary><i>XR ⚙️</i></summary>

```yaml
---
apiVersion: resource-merger.fn.canilho.net/v1alpha1
kind: XR
metadata:
  name: merger-results-xr
spec:
  options:
    override: true
    appendSlice: true
    sliceDeepCopy: true
  transform:
    stringToMap: true
```

</details>

<details> 
    <summary><i>Function ⚙️</i></summary>

```yaml
---
apiVersion: pkg.crossplane.io/v1beta1
kind: Function
metadata:
  name: function-xresources-merger
  annotations:
    # This tells crossplane beta render to connect to the function locally.
    render.crossplane.io/runtime: Development
spec:
  # This is ignored when using the Development runtime.
  package: function-xresources-merger

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
    annotations:
      crossplane.io/composition-resource-name: merged
      generateName: merger-results-xr-
  ...
  ```

### Test it out!

1. Launch the function locally in a separate shell:
    ```shell
      go run . --insecure --debug
    ```
2. Render the example:
    ```shell
    cd example && crossplane beta render xr.yaml composition.yaml functions.yaml -r
    ```

##### References

* `functions`: https://docs.crossplane.io/latest/concepts/composition-functions
* `go`: https://go.dev
* `docker`: https://www.docker.com
* `cli`: https://docs.crossplane.io/latest/cli
