package transformer

import (
	"fmt"
	"strings"

	"github.com/crossplane/function-sdk-go/resource"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime"
)

type IO = map[string]any

func Transform(xr *resource.Composite, in IO) (IO, error) {
	type xrSpec struct {
		Spec struct {
			Transform map[string]bool
		}
	}

	out := in
	transformerMap := map[string]func(IO) IO{
		"stringToMap": transformData,
	}

	var xrConfig xrSpec
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

// transformData transforms a map of strings to a map of any.
func transformData(a map[string]any) map[string]any {
	outData := make(map[string]any)
	for k, v := range a {
		dataConverted := make(map[string]any)
		if err := yaml.NewDecoder(strings.NewReader(fmt.Sprint(v))).Decode(dataConverted); err != nil {
			outData[k] = v
		} else {
			outData[k] = dataConverted
		}
	}
	return outData
}
