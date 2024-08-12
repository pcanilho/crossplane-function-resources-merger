// Package merger provides utilities for merging resources.
package merger

import (
	"dario.cat/mergo"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/crossplane/function-sdk-go/resource"
)

// ParseMergoOpts parses the options from the XR and returns a map of mergo options
func ParseMergoOpts(xr *resource.Composite) (out map[string]func(*mergo.Config), err error) {
	out = make(map[string]func(*mergo.Config))
	if xr == nil {
		return out, nil
	}
	optsMap := map[string]func(*mergo.Config){
		"override":            mergo.WithOverride,
		"appendSlice":         mergo.WithAppendSlice,
		"sliceDeepCopy":       mergo.WithSliceDeepCopy,
		"overwriteEmptyValue": mergo.WithOverwriteWithEmptyValue,
		"overrideEmptySlice":  mergo.WithOverrideEmptySlice,
		"typeCheck":           mergo.WithTypeCheck,
	}

	type xrSpec struct {
		Spec struct {
			Options map[string]bool
		}
	}

	var xrConfig xrSpec
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(xr.Resource.Object, &xrConfig); err != nil {
		return nil, errors.Wrap(err, "cannot convert XR to struct")
	}

	for opt, enabled := range xrConfig.Spec.Options {
		if !enabled {
			continue
		}
		if o, ok := optsMap[opt]; ok {
			out[opt] = o
		}
	}
	return out, nil
}
