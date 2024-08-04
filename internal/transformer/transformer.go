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

// Transform parses a given XR composite and applies any found settings by running the appropriate transformer.
func Transform(xr *resource.Composite, in io) (io, error) {
	type xrSpec struct {
		Spec struct {
			Transform map[string]bool
		}
	}

	out := in
	transformerMap := map[string]func(io) io{
		"stringToMap": transformToMap,
	}

	var xrConfig xrSpec
	//nolint: nilerr // Silently ignore not set transform settings
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

// transformToMap transforms a map of strings to a map of any.
func transformToMap(a map[string]any) map[string]any {
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

// TransformFromMap asserts that all map values are strings
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
