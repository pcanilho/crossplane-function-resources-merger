package main

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/crossplane/function-sdk-go/logging"
	fnv1beta1 "github.com/crossplane/function-sdk-go/proto/v1beta1"
	"github.com/crossplane/function-sdk-go/resource"
	"github.com/crossplane/function-sdk-go/response"
)

func TestRunFunction(t *testing.T) {
	type args struct {
		ctx context.Context
		req *fnv1beta1.RunFunctionRequest
	}
	type want struct {
		rsp *fnv1beta1.RunFunctionResponse
		err error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"NoResourcesRefProvided": {
			args: args{
				ctx: context.Background(),
				req: &fnv1beta1.RunFunctionRequest{
					Meta: &fnv1beta1.RequestMeta{Tag: "test"},
					Input: resource.MustStructJSON(`{
						"apiVersion": "resources-merger.fn.canilho.net/v1alpha1",
						"kind": "Input",
						"targetRef": {
							"apiVersion": "v1",
							"kind": "ConfigMap",
							"namespace": "ephemeral"
						}
					}`),
				},
			},
			want: want{
				rsp: &fnv1beta1.RunFunctionResponse{
					Meta: &fnv1beta1.ResponseMeta{Tag: "test", Ttl: durationpb.New(response.DefaultTTL)},
					Results: []*fnv1beta1.Result{
						{
							Severity: fnv1beta1.Severity_SEVERITY_FATAL,
							Message:  "no resources to merge",
						},
					},
				},
			},
		},
		"NoTargetRefNamespace": {
			args: args{
				ctx: context.Background(),
				req: &fnv1beta1.RunFunctionRequest{
					Meta: &fnv1beta1.RequestMeta{Tag: "test"},
					Input: resource.MustStructJSON(`{
						"apiVersion": "resources-merger.fn.canilho.net/v1alpha1",
						"kind": "Input"
					}`),
				},
			},
			want: want{
				rsp: &fnv1beta1.RunFunctionResponse{
					Meta: &fnv1beta1.ResponseMeta{Tag: "test", Ttl: durationpb.New(response.DefaultTTL)},
					Results: []*fnv1beta1.Result{
						{
							Severity: fnv1beta1.Severity_SEVERITY_FATAL,
							Message:  "no target namespace to create the composed resource",
						},
					},
				},
			},
		},
		"NoTargetRefGVK": {
			args: args{
				ctx: context.Background(),
				req: &fnv1beta1.RunFunctionRequest{
					Meta: &fnv1beta1.RequestMeta{Tag: "test"},
					Input: resource.MustStructJSON(`{
						"apiVersion": "resources-merger.fn.canilho.net/v1alpha1",
						"kind": "Input",
						"targetRef": {
							"namespace": "map-merged"
						}
					}`),
				},
			},
			want: want{
				rsp: &fnv1beta1.RunFunctionResponse{
					Meta: &fnv1beta1.ResponseMeta{Tag: "test", Ttl: durationpb.New(response.DefaultTTL)},
					Results: []*fnv1beta1.Result{
						{
							Severity: fnv1beta1.Severity_SEVERITY_FATAL,
							Message:  "no target resource group version kind",
						},
					},
				},
			},
		},
		"ResourceRefsNotFound": {
			args: args{
				ctx: context.Background(),
				req: &fnv1beta1.RunFunctionRequest{
					Meta: &fnv1beta1.RequestMeta{Tag: "test"},
					Input: resource.MustStructJSON(`{
						"apiVersion": "resources-merger.fn.canilho.net/v1alpha1",
						"kind": "Input",
						"targetRef": {
							"apiVersion": "v1",
							"kind": "ConfigMap",
							"namespace": "ephemeral"
						},
						"resourceRefs": [
							{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"name": "invalid-map",
								"namespace": "ephemeral"
							},
							{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"name": "map-2",
								"namespace": "ephemeral"
							}
						]
					}`),
				},
			},
			want: want{
				rsp: &fnv1beta1.RunFunctionResponse{
					Meta: &fnv1beta1.ResponseMeta{Tag: "test", Ttl: durationpb.New(response.DefaultTTL)},
					Results: []*fnv1beta1.Result{
						{
							Severity: fnv1beta1.Severity_SEVERITY_FATAL,
							Message:  "failed to find resourceRef: ConfigMap/invalid-map: failed to get resource: the server could not find the requested resource",
						},
					},
				},
			},
		},
		"FoundAndMerged": {
			args: args{
				ctx: context.Background(),
				req: &fnv1beta1.RunFunctionRequest{
					Observed: &fnv1beta1.State{
						Composite: &fnv1beta1.Resource{
							Resource: resource.MustStructJSON(`{
								"apiVersion": "resources-merger.fn.canilho.net/v1alpha1",
								"kind": "XR",
								"metadata": {
									"name": "merger-results-xr"
								},
								"spec": {
									"options": {
										"override": true,
										"appendSlice": true,
										"sliceDeepCopy": true
									}
								}
							}`),
						},
					},
					Meta: &fnv1beta1.RequestMeta{Tag: "test"},
					Input: resource.MustStructJSON(`{
						"apiVersion": "resources-merger.fn.canilho.net/v1alpha1",
						"kind": "Input",
						"targetRef": {
							"apiVersion": "v1",
							"kind": "ConfigMap",
							"name": "map-merged",
							"namespace": "ephemeral"
						},
						"resourceRefs": [
							{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"name": "map-1",
								"namespace": "ephemeral"
							},
							{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"name": "map-2",
								"namespace": "ephemeral"
							}
						]
					}`),
				},
			},
			want: want{
				rsp: &fnv1beta1.RunFunctionResponse{
					Meta: &fnv1beta1.ResponseMeta{Tag: "test", Ttl: durationpb.New(response.DefaultTTL)},
					Results: []*fnv1beta1.Result{
						{
							Severity: fnv1beta1.Severity_SEVERITY_NORMAL,
							Message:  "Successfully composed resource [external-name=map-merged] [resource=/v1, Kind=ConfigMap] [namespace=ephemeral]",
						},
					},
					Desired: &fnv1beta1.State{
						Resources: map[string]*fnv1beta1.Resource{
							"xmerger-map-merged": {
								Resource: resource.MustStructJSON(`{
									"apiVersion": "v1",
									"kind": "ConfigMap",
									"metadata": {
										"namespace": "ephemeral",
										"name": "map-merged",
										"annotations": {
											"crossplane.io/external-name": "map-merged"
										}
									},
									"data": {
										"key1": "a",
										"key2": "c",
										"key4": "d"
									}
								}`),
							},
						},
					},
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &Function{log: logging.NewNopLogger()}
			rsp, err := f.RunFunction(tc.args.ctx, tc.args.req)
			if rsp != nil && rsp.GetDesired() != nil && rsp.GetDesired().GetResources() != nil {
				delete(rsp.GetDesired().GetResources()["map-merged"].GetResource().GetFields(), "metadata")
			}
			if diff := cmp.Diff(tc.want.rsp, rsp, protocmp.Transform()); diff != "" {
				t.Errorf("%s\nf.RunFunction(...): -want rsp, +got rsp:\n%s", tc.reason, diff)
			}

			if diff := cmp.Diff(tc.want.err, err, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("%s\nf.RunFunction(...): -want err, +got err:\n%s", tc.reason, diff)
			}
		})
	}
}
