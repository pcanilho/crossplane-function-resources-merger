[![CI](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/ci.yaml/badge.svg)](https://github.com/pcanilho/crossplane-function-resources-merger/actions/workflows/ci.yaml)
<p align="center" width="100%">
    <img src="https://github.com/pcanilho/crossplane-function-resources-merger/blob/main/docs/images/banner.png?raw=true" width="220"></img>
    <br>
    <i><b>function-resources-merger</b></i>
    <br>
    An arbitrary resources merging function for Crossplane.
    <br>
    <br>
    🔎 <a href="#how-to-use">How-to-use</a> | 🚀 <a href="#example">Get started with an Example</a>
    <br>
</p>

> [!IMPORTANT]
> This function is under development and is not yet ready for production use.

This is a crossplane function that merges any Kubernetes resources which contain a root `data` field in their spec and
produces a single resulting resource containing the merged result.

## How-to-use

### Function `Input` specification

`targetRef` (required)

This field specifies the target resource that will be created/managed by this function.

| Field        | Description                                                                                 |
|--------------|---------------------------------------------------------------------------------------------|
| `namespace`  | The namespace where the target resource will be created/managed.                            |
| `name`       | The name of the target composition resource name `crossplane.io/composition-resource-name`. |
| `apiVersion` | The API version of the target resource.                                                     |
| `kind`       | The kind of the target resource.                                                            |

`resourceRefs` (required)
A list of resources that will be used to merge into the target resource.

| Field            | Description                                               |
|------------------|-----------------------------------------------------------|
| `namespace`      | The namespace where the resource is located.              |
| `name`           | The name of the resource.                                 |
| `apiVersion`     | The API version of the resource.                          |
| `kind`           | The kind of the resource.                                 |
| `extractFromKey` | (Optional) The key to extract the data from the resource. |

> ![TIP]
> Both `targetRef` and `resourceRefs` have full support for both standard kubernetes resources and custom-resources.

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
> ➤ **options**
> | Option | Description |
> | --- | --- |
> | `override` | Merge override non-empty dst attributes with non-empty src attributes values. |
> | `typeCheck` | Merge check types while overwriting it (must be used with `override`). |
> | `appendSlice` | Merge append slices instead of overwriting it. |
> | `sliceDeepCopy` | Merge slice element one by one with Overwrite flag. |
> | `overwriteEmptyValue` | Merge override non-empty dst attributes with empty src attributes values. |
> | `overrideEmptySlice` | Merge override empty dst slice with empty src slice. |
>
> ➤ **transform**
> | Option | Description |
> | --- | --- |
> | `stringToMap` | String values will be transformed to the maps when possible. Allowing for deep-merging. |

## Example

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
        apiVersion: resources-merged.fn.canilho.net/v1alpha1
        kind: Input
        targetRef:
          namespace: ephemeral
          name: merged
          apiVersion: v1
          kind: ConfigMap
        resourceRefs:
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
