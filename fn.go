package main

import (
	"context"

	"dario.cat/mergo"
	"github.com/pcanilho/crossplane-function-xresources-merger/input/v1alpha1"
	"github.com/pcanilho/crossplane-function-xresources-merger/internal/k8s"
	"github.com/pcanilho/crossplane-function-xresources-merger/internal/maps"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/crossplane/crossplane-runtime/pkg/errors"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	fnv1beta1 "github.com/crossplane/function-sdk-go/proto/v1beta1"
	"github.com/crossplane/function-sdk-go/request"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/resource/composed"
	"github.com/crossplane/function-sdk-go/response"
)

// Function returns whatever response you ask it to.
type Function struct {
	fnv1beta1.UnimplementedFunctionRunnerServiceServer

	log logging.Logger
}

// RunFunction runs the Function.
func (f *Function) RunFunction(ctx context.Context, req *fnv1beta1.RunFunctionRequest) (*fnv1beta1.RunFunctionResponse, error) {
	f.log.Info("Running function", "tag", req.GetMeta().GetTag())

	rsp := response.To(req, response.DefaultTTL)

	in := &v1alpha1.Input{}
	if err := request.GetInput(req, in); err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot get Function input from %T", req))
		return rsp, nil
	}

	if in.TargetRef.Namespace == "" {
		response.Fatal(rsp, errors.New("no target namespace to create the composed resource"))
		return rsp, nil
	}

	if in.TargetRef.Ref.APIVersion == "" || in.TargetRef.Ref.Kind == "" {
		response.Fatal(rsp, errors.New("no target resource group version kind"))
		return rsp, nil
	}

	if in.ResourceRefs == nil || len(in.ResourceRefs) == 0 {
		response.Fatal(rsp, errors.New("no resources to merge"))
		return rsp, nil
	}

	xr, err := request.GetObservedCompositeResource(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot get observed composite resource from %T", req))
		return rsp, nil
	}

	f.log.Info("Running function...", "observed", xr.Resource.Object)
	mergoOpts, err := parseMergoOpts(xr)
	if err != nil {
		f.log.Info("Failed to parse mergo options from XR", "error", err)
		response.Fatal(rsp, errors.Wrap(err, "cannot parse mergo options from XR"))
		return rsp, nil
	}
	f.log.Info("Parsed merging options...", "options", maps.Keys(mergoOpts))

	k8cCtl, err := k8s.NewController(k8s.WithTimeout(response.DefaultTTL))
	if err != nil {
		response.Fatal(rsp, errors.Wrap(err, "cannot create Kubernetes controller"))
		return rsp, nil
	}

	var mergedResource map[string]any
	for _, ref := range in.ResourceRefs {
		f.log.Debug("Attempting to find resource...", "GroupVersionKind", ref.Ref.GroupVersionKind(), "Name", ref.Ref.Name, "Namespace", ref.Namespace)
		res, err := k8cCtl.GetResource(ctx, ref.Namespace, ref.Ref.Name, ref.Ref.GroupVersionKind(), v1.GetOptions{
			TypeMeta: in.TypeMeta,
		})
		if err != nil {
			response.Fatal(rsp, errors.Wrapf(err, "failed to find resourceRef: %s/%s", ref.Ref.Kind, ref.Ref.Name))
			return rsp, nil
		}
		uRes, err := runtime.DefaultUnstructuredConverter.ToUnstructured(res)
		if err != nil {
			response.Fatal(rsp, errors.Wrap(err, "cannot convert resource to unstructured"))
			return rsp, nil
		}
		if _, ok := uRes["data"]; !ok {
			response.Fatal(rsp, errors.New("resource is not merge-able as it does not have a data field"))
			return rsp, nil
		}

		if mergedResource == nil {
			mergedResource = uRes["data"].(map[string]any)
			continue
		}

		existingData := mergedResource
		toMergeData := uRes["data"].(map[string]any)
		f.log.Info("Merging data [a←b]...", "a:len", len(existingData), "b:len", len(toMergeData))
		if mergeErr := mergo.Merge(&existingData, toMergeData, maps.Values(mergoOpts)...); mergeErr != nil {
			response.Fatal(rsp, errors.Wrap(mergeErr, "cannot merge resources"))
			return rsp, nil
		}
		mergedResource = existingData
	}

	target := in.TargetRef
	gvk := target.Ref.GroupVersionKind()
	runtimeObject := &unstructured.Unstructured{Object: map[string]any{"data": mergedResource}}
	runtimeObject.SetGroupVersionKind(gvk)

	desired, err := request.GetDesiredComposedResources(req)
	if err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot get desired resources from %T", req))
		return rsp, nil
	}

	composed.Scheme.AddKnownTypeWithName(gvk, runtimeObject)
	dc, err := composed.From(runtimeObject)
	if err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "Unable to compose resource"))
		return rsp, nil
	}

	desired[resource.Name(in.TargetRef.Ref.Name)] = &resource.DesiredComposed{Resource: dc}
	if err = response.SetDesiredComposedResources(rsp, desired); err != nil {
		response.Fatal(rsp, errors.Wrapf(err, "cannot set desired composed resources in %T", rsp))
		return rsp, nil
	}
	response.Normalf(rsp, "Successfully composed resource [resource=%s] [namespace=%s]", in.TargetRef.Ref.GroupVersionKind(), in.TargetRef.Namespace)
	f.log.Info("Successfully composed resource...", "resource", in.TargetRef.Ref.GroupVersionKind(), "namespace", in.TargetRef.Namespace)
	return rsp, nil
}

func parseMergoOpts(xr *resource.Composite) (out map[string]func(*mergo.Config), err error) {
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
