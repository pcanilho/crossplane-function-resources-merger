// Package transformer offers utility functions to transform data.
package transformer

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/crossplane/function-sdk-go/resource"
)

type io = map[string]any

var transformerMap = map[string]func(io) io{
	"stringToMap": TransformToMap,
}

// Transform parses a given XR composite and applies any found settings by running the appropriate transformer.
func Transform(xr *resource.Composite, in io) (io, error) {
	type xrSpec struct {
		Spec struct {
			Transform map[string]bool
		}
	}

	out := in
	var xrConfig xrSpec
	//nolint: nilerr // Silently ignore when transform settings are not set
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(xr.Resource.Object, &xrConfig); err != nil {
		return in, nil
	}

	for opt, enabled := range xrConfig.Spec.Transform {
		if !enabled {
			continue
		}
		if transformFn, ok := transformerMap[opt]; ok {
			out = transformFn(out)
		}
	}

	return out, nil
}

// TransformToMap transforms all map values to other maps when possible.
func TransformToMap(a map[string]any) map[string]any {
	outData := make(map[string]any)
	for k, v := range a {
		if vs, ok := v.(string); ok {
			dataConverted := make(map[string]any)
			if err := yaml.NewDecoder(strings.NewReader(vs)).Decode(dataConverted); err == nil {
				outData[k] = dataConverted
				continue
			}
		}
		outData[k] = v
	}
	return outData
}

// TransformFromMap transforms all map values to string.
func TransformFromMap(a map[string]any) map[string]any {
	outData := make(map[string]any)
	for k, v := range a {
		if vm, ok := v.(map[string]any); ok {
			var buf strings.Builder
			if err := yaml.NewEncoder(&buf).Encode(vm); err == nil {
				outData[k] = buf.String()
				continue
			}
		}
		outData[k] = fmt.Sprint(v)
	}
	return outData
}

// ExtractMapValue extracts a map value where its key matches the provided argument.
func ExtractMapValue(m map[string]any, key string) (map[string]any, error) {
	for k, v := range m {
		vm, ok := v.(map[string]any)
		if k == key {
			if ok {
				return vm, nil
			}
			return map[string]any{k: v}, nil
		}
		if ok {
			return ExtractMapValue(vm, key)
		}
	}
	return nil, fmt.Errorf("unable to find value for key [%s]", key)
}
