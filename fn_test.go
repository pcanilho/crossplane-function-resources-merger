package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/durationpb"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/crossplane/function-sdk-go/logging"
	fnv1 "github.com/crossplane/function-sdk-go/proto/v1"
	"github.com/crossplane/function-sdk-go/resource"
)

const (
	testTag = "test"

	// mergedKey is the desired state key derived from the target's identity,
	// <kind>[.<group>]/[<namespace>/]<name>. ConfigMap is in the core group, so
	// there is no group segment.
	mergedKey = "configmap/ephemeral/merged"

	// envConfigKey is the same encoding for a cluster scoped kind in a real API
	// group. Note the group and the absence of the API version.
	envConfigKey = "environmentconfig.apiextensions.crossplane.io/merged"

	// groupedKey is a namespaced kind in a real API group.
	groupedKey = "configmap.example.org/ephemeral/merged"

	// These keep repeated literals in one place. nameMerged and nsEphemeral are
	// only used where the identity itself is under test.
	kindConfigMap   = "ConfigMap"
	nsEphemeral     = "ephemeral"
	nameMerged      = "merged"
	mentionsSourceA = `source "a"`
	mergedType      = "Merged"

	// twoSourceInput merges two namespaced ConfigMaps into a third.
	twoSourceInput = `{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
		"sources": [
			{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}},
			{"name": "b", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-2", "namespace": "platform"}}
		]
	}`

	// oneSourceInput has the same target and only the first source.
	oneSourceInput = `{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
		"sources": [
			{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
		]
	}`

	sourceA = `{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {"name": "map-1", "namespace": "platform"},
		"data": {"shared": "from-a", "only-a": "1"}
	}`

	// nsXRInput places its source in the composite's own namespace so it stays
	// valid once cross namespace source reads are confined. target.namespace
	// deliberately disagrees with the composite, to exercise the ignore path.
	nsXRInput = `{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
		"sources": [
			{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "team-a"}}
		]
	}`

	sourceATeamA = `{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {"name": "map-1", "namespace": "team-a"},
		"data": {"shared": "from-a", "only-a": "1"}
	}`

	sourceB = `{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {"name": "map-2", "namespace": "platform"},
		"data": {"shared": "from-b", "only-b": "2"}
	}`
)

// twoSourceRequirements is what twoSourceInput must always ask Crossplane for.
func twoSourceRequirements() *fnv1.Requirements {
	return &fnv1.Requirements{Resources: map[string]*fnv1.ResourceSelector{
		"a": {
			ApiVersion: "v1",
			Kind:       kindConfigMap,
			Match:      &fnv1.ResourceSelector_MatchName{MatchName: "map-1"},
			Namespace:  new("platform"),
		},
		"b": {
			ApiVersion: "v1",
			Kind:       kindConfigMap,
			Match:      &fnv1.ResourceSelector_MatchName{MatchName: "map-2"},
			Namespace:  new("platform"),
		},
	}}
}

func oneSourceRequirements() *fnv1.Requirements {
	return &fnv1.Requirements{Resources: map[string]*fnv1.ResourceSelector{
		"a": {
			ApiVersion: "v1",
			Kind:       kindConfigMap,
			Match:      &fnv1.ResourceSelector_MatchName{MatchName: "map-1"},
			Namespace:  new("platform"),
		},
	}}
}

// mergedCondition is the Merged condition RunFunction sets on every successful
// merge.
func mergedCondition(msg string) []*fnv1.Condition {
	return []*fnv1.Condition{{
		Type:    mergedType,
		Status:  fnv1.Status_STATUS_CONDITION_TRUE,
		Reason:  "Success",
		Target:  fnv1.Target_TARGET_COMPOSITE_AND_CLAIM.Enum(),
		Message: new(msg),
	}}
}

// items wraps resolved required resources as Crossplane sends them.
func items(json ...string) *fnv1.Resources {
	out := &fnv1.Resources{Items: make([]*fnv1.Resource, 0, len(json))}
	for _, j := range json {
		out.Items = append(out.Items, &fnv1.Resource{Resource: resource.MustStructJSON(j)})
	}
	return out
}

// fatal is the response shape for an input that fails validation, before any
// requirement is built.
func fatal() *fnv1.RunFunctionResponse {
	return &fnv1.RunFunctionResponse{
		Meta: &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
		Results: []*fnv1.Result{{
			Severity: fnv1.Severity_SEVERITY_FATAL,
			Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
		}},
	}
}

// req builds a request from an Input and optional resolved requirements.
func req(input string, required map[string]*fnv1.Resources) *fnv1.RunFunctionRequest {
	return &fnv1.RunFunctionRequest{
		Meta:              &fnv1.RequestMeta{Tag: testTag},
		Input:             resource.MustStructJSON(input),
		RequiredResources: required,
	}
}

func TestRunFunction(t *testing.T) {
	type args struct {
		ctx context.Context
		req *fnv1.RunFunctionRequest
	}
	type want struct {
		rsp *fnv1.RunFunctionResponse
		err error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
		// mentions relaxes the comparison for cases whose message wording is an
		// implementation detail. Result.message is then excluded from the diff
		// and only checked for these substrings.
		mentions []string
	}{
		"ReturnsRequirementsWhenSourcesUnresolved": {
			reason: "With no required resources in the request, Crossplane has not looked yet, so the function returns its requirements and no desired resources.",
			args:   args{ctx: context.Background(), req: req(twoSourceInput, nil)},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: twoSourceRequirements(),
				},
			},
		},
		"MergesWhenSourcesResolved": {
			reason: "Once Crossplane resolves both sources they are merged in declared order, so the later source wins.",
			args: args{ctx: context.Background(), req: req(twoSourceInput, map[string]*fnv1.Resources{
				"a": items(sourceA),
				"b": items(sourceB),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: twoSourceRequirements(),
					Conditions:   mergedCondition("2 of 2 sources merged"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						mergedKey: {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"metadata": {"name": "merged", "namespace": "ephemeral"},
								"data": {"shared": "from-b", "only-a": "1", "only-b": "2"}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"PreservesEarlierPipelineStepsDesiredResources": {
			reason: "What earlier pipeline steps put in desired state must be read and written back, not clobbered.",
			args: args{
				ctx: context.Background(),
				req: &fnv1.RunFunctionRequest{
					Meta:  &fnv1.RequestMeta{Tag: testTag},
					Input: resource.MustStructJSON(twoSourceInput),
					RequiredResources: map[string]*fnv1.Resources{
						"a": items(sourceA),
						"b": items(sourceB),
					},
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						"unrelated": {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"metadata": {"name": "from-an-earlier-step", "namespace": "ephemeral"},
								"data": {"k": "v"}
							}`),
						},
					}},
				},
			},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: twoSourceRequirements(),
					Conditions:   mergedCondition("2 of 2 sources merged"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						"unrelated": {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"metadata": {"name": "from-an-earlier-step", "namespace": "ephemeral"},
								"data": {"k": "v"}
							}`),
						},
						mergedKey: {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"metadata": {"name": "merged", "namespace": "ephemeral"},
								"data": {"shared": "from-b", "only-a": "1", "only-b": "2"}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"OptionalSourceAbsentIsSkipped": {
			reason: "A resolved but empty requirement for an Optional source is a steady state, so it is skipped silently with no result.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "resolution": "Optional", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}},
					{"name": "b", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-2", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{
				"a": items(),
				"b": items(sourceB),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: twoSourceRequirements(),
					Conditions:   mergedCondition("1 of 2 sources merged; skipped optional: a"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						mergedKey: {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"metadata": {"name": "merged", "namespace": "ephemeral"},
								"data": {"shared": "from-b", "only-b": "2"}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"RequiredSourceAbsentIsFatal": {
			reason: "A resolved but empty requirement for a Required source is fatal, naming the source.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "resolution": "Required", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}},
					{"name": "b", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-2", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{
				"a": items(),
				"b": items(sourceB),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: twoSourceRequirements(),
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{`"a"`, "map-1"},
		},
		"SourceDataNullIsFatalNotPanic": {
			reason: "A source whose data is null must be a fatal result, not a panic that kills the shared function pod.",
			args: args{ctx: context.Background(), req: req(oneSourceInput, map[string]*fnv1.Resources{
				"a": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-1"}, "data": null}`),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{mentionsSourceA, `"data"`},
		},
		"SourceDataListIsFatalNotPanic": {
			reason: "A source whose data is a list must be a fatal result, not a panic.",
			args: args{ctx: context.Background(), req: req(oneSourceInput, map[string]*fnv1.Resources{
				"a": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-1"}, "data": ["a", "b"]}`),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{mentionsSourceA, `"data"`},
		},
		"SourceDataStringIsFatalNotPanic": {
			reason: "A fromFieldPath resolving to a scalar is fatal, naming the source and the path.",
			args: args{ctx: context.Background(), req: req(oneSourceInput, map[string]*fnv1.Resources{
				"a": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-1"}, "data": "not-an-object"}`),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{mentionsSourceA, `"data"`},
		},
		"AbsentFieldRequiredContributesNothing": {
			reason: "A source that exists but has no field at fromFieldPath contributes nothing under Required: not fatal, not counted, but named in the condition under its own clause.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "resolution": "Required", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{
				"a": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-1", "namespace": "platform"}}`),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Conditions:   mergedCondition("0 of 1 sources merged; no data: a"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						mergedKey: {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"metadata": {"name": "merged", "namespace": "ephemeral"},
								"data": {}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"AbsentFieldOptionalContributesNothing": {
			reason: "resolution governs whether the resource must exist, not whether its field must be present: Optional behaves identically to Required for an absent field.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "resolution": "Optional", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{
				"a": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-1", "namespace": "platform"}}`),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Conditions:   mergedCondition("0 of 1 sources merged; no data: a"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						mergedKey: {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"metadata": {"name": "merged", "namespace": "ephemeral"},
								"data": {}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"SourceDataStringIsFatalNotPanicUnderOptional": {
			reason: "A type error is not absence: still fatal under Optional, proving resolution never softens it.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "resolution": "Optional", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{
				"a": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-1"}, "data": "not-an-object"}`),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{mentionsSourceA, `"data"`},
		},
		"MalformedFromFieldPathIsFatalRequired": {
			reason: "A malformed fromFieldPath fails to parse, so fieldpath.IsNotFound is false: fatal under Required, proving discrimination is on IsNotFound, not on err != nil.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "resolution": "Required", "fromFieldPath": "data..bad", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{
				"a": items(sourceA),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{mentionsSourceA, "data..bad"},
		},
		"MalformedFromFieldPathIsFatalOptional": {
			reason: "Same malformed path under Optional: still fatal, proving the same discrimination holds regardless of resolution.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "resolution": "Optional", "fromFieldPath": "data..bad", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{
				"a": items(sourceA),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{mentionsSourceA, "data..bad"},
		},
		"SecretSourceIntoConfigMapTargetIsFatal": {
			reason: "Secret data is base64, so merging it into a ConfigMap would emit garbage and leak the secret in plaintext.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "Secret", "name": "creds", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{mentionsSourceA, "Secret"},
		},
		"AllowedSourceNamespacesRejectsEmptyString": {
			reason: "An empty string cannot name a real namespace and reads as a typo for one, so it is rejected outright rather than silently matching nothing.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"allowedSourceNamespaces": [""],
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"allowedSourceNamespaces", "empty namespace"},
		},
		"TargetMetadataLandsOnTheComposedResource": {
			reason: "target.metadata carries labels and annotations onto the merged resource.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {
					"apiVersion": "v1",
					"kind": "ConfigMap",
					"name": "merged",
					"namespace": "ephemeral",
					"metadata": {
						"labels": {"config.acme.io/role": "merged"},
						"annotations": {"config.acme.io/owner": "platform"}
					}
				},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{"a": items(sourceA)})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Conditions:   mergedCondition("1 of 1 sources merged"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						mergedKey: {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"metadata": {
									"name": "merged",
									"namespace": "ephemeral",
									"labels": {"config.acme.io/role": "merged"},
									"annotations": {"config.acme.io/owner": "platform"}
								},
								"data": {"shared": "from-a", "only-a": "1"}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"TargetNameFromCompositeFieldPath": {
			reason: "A Composition author names the path; the desired key and metadata.name both follow the value it resolves to.",
			args: args{
				ctx: context.Background(),
				req: &fnv1.RunFunctionRequest{
					Meta: &fnv1.RequestMeta{Tag: testTag},
					Observed: &fnv1.State{Composite: &fnv1.Resource{
						Resource: resource.MustStructJSON(`{
							"apiVersion": "example.org/v1",
							"kind": "XMerger",
							"metadata": {"name": "merger-xr"},
							"spec": {"appName": "billing"}
						}`),
					}},
					Input: resource.MustStructJSON(`{
						"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
						"kind": "Input",
						"target": {"apiVersion": "v1", "kind": "ConfigMap", "nameFromCompositeFieldPath": "spec.appName", "namespace": "ephemeral"},
						"sources": [
							{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
						]
					}`),
					RequiredResources: map[string]*fnv1.Resources{"a": items(sourceA)},
				},
			},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Conditions:   mergedCondition("1 of 1 sources merged"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						"configmap/ephemeral/billing": {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "ConfigMap",
								"metadata": {"name": "billing", "namespace": "ephemeral"},
								"data": {"shared": "from-a", "only-a": "1"}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"ClusterScopedSourceHasNilRequirementNamespace": {
			reason: "An empty source namespace becomes a nil selector namespace, which is how a cluster scoped kind is expressed.",
			args:   args{ctx: context.Background(), req: req(clusterScopedInput, nil)},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: clusterScopedRequirements(),
				},
			},
		},
		"ClusterScopedTargetComposes": {
			reason: "EnvironmentConfig is cluster scoped, so the key omits the namespace and no ConfigMap lowering happens.",
			args: args{ctx: context.Background(), req: req(clusterScopedInput, map[string]*fnv1.Resources{
				"a": items(`{
					"apiVersion": "apiextensions.crossplane.io/v1beta1",
					"kind": "EnvironmentConfig",
					"metadata": {"name": "env-1"},
					"data": {"region": "eu-west-1", "replicas": 3}
				}`),
			})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: clusterScopedRequirements(),
					Conditions:   mergedCondition("1 of 1 sources merged"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						envConfigKey: {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "apiextensions.crossplane.io/v1beta1",
								"kind": "EnvironmentConfig",
								"metadata": {"name": "merged"},
								"data": {"region": "eu-west-1", "replicas": 3}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"GroupedTargetKeyCarriesTheGroupNotTheAPIVersion": {
			reason: "A kind alone is not an identity, so the key carries the group. It carries the group and not the raw apiVersion, which would smuggle a slash into one component.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "example.org/v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{"a": items(sourceA)})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Conditions:   mergedCondition("1 of 1 sources merged"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						groupedKey: {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "example.org/v1",
								"kind": "ConfigMap",
								"metadata": {"name": "merged", "namespace": "ephemeral"},
								"data": {"shared": "from-a", "only-a": "1"}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"TargetNameAndFieldPathTogetherIsFatal": {
			reason: "target.name and target.nameFromCompositeFieldPath are mutually exclusive.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "nameFromCompositeFieldPath": "spec.appName", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"target.name", "target.nameFromCompositeFieldPath"},
		},
		"TargetNamespaceAndFieldPathTogetherIsFatal": {
			reason: "target.namespace and target.namespaceFromCompositeFieldPath are mutually exclusive.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {
					"apiVersion": "v1",
					"kind": "ConfigMap",
					"name": "merged",
					"namespace": "ephemeral",
					"namespaceFromCompositeFieldPath": "metadata.namespace"
				},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"target.namespace", "target.namespaceFromCompositeFieldPath"},
		},
		"TargetWithNoNameIsFatal": {
			reason: "Without a name the desired key stops identifying the target, which is the defect the derived key exists to prevent.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"target.name", "target.nameFromCompositeFieldPath"},
		},
		"NameFieldPathResolvingToEmptyIsFatal": {
			reason: "fieldpath returns no error for a present but empty field, so an empty name needs its own guard.",
			args: args{
				ctx: context.Background(),
				req: &fnv1.RunFunctionRequest{
					Meta: &fnv1.RequestMeta{Tag: testTag},
					Observed: &fnv1.State{Composite: &fnv1.Resource{
						Resource: resource.MustStructJSON(`{
							"apiVersion": "example.org/v1",
							"kind": "XMerger",
							"metadata": {"name": "merger-xr"},
							"spec": {"appName": ""}
						}`),
					}},
					Input: resource.MustStructJSON(`{
						"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
						"kind": "Input",
						"target": {"apiVersion": "v1", "kind": "ConfigMap", "nameFromCompositeFieldPath": "spec.appName", "namespace": "ephemeral"},
						"sources": [
							{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
						]
					}`),
				},
			},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{"target.nameFromCompositeFieldPath", "spec.appName"},
		},
		"NamespaceFieldPathResolvingToEmptyIsFatal": {
			reason: "An empty namespace would silently make the target cluster scoped and drop the namespace from the key.",
			args: args{
				ctx: context.Background(),
				req: &fnv1.RunFunctionRequest{
					Meta: &fnv1.RequestMeta{Tag: testTag},
					Observed: &fnv1.State{Composite: &fnv1.Resource{
						Resource: resource.MustStructJSON(`{
							"apiVersion": "example.org/v1",
							"kind": "XMerger",
							"metadata": {"name": "merger-xr"},
							"spec": {"targetNamespace": ""}
						}`),
					}},
					Input: resource.MustStructJSON(`{
						"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
						"kind": "Input",
						"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespaceFromCompositeFieldPath": "spec.targetNamespace"},
						"sources": [
							{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
						]
					}`),
				},
			},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{"target.namespaceFromCompositeFieldPath", "spec.targetNamespace"},
		},
		"SourceWithNoNameIsFatal": {
			reason: "A source name is the requirement key, so an empty one would collapse sources into a single slot.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"name"},
		},
		"SourceWithNoRefGVKIsFatal": {
			reason: "A selector with no group version kind cannot resolve.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{mentionsSourceA},
		},
		"SourceWithNoRefNameIsFatal": {
			reason: "A MatchName selector with an empty name resolves to nothing, which would surface later as a confusing absent source.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{mentionsSourceA},
		},
		"UnknownMergeStrategyIsFatal": {
			reason: "A typo in mergeStrategy must be caught during validation, since a single source never reaches the merge.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"mergeStrategy": "ForceMergeObject",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"ForceMergeObject"},
		},
		"NoSourcesIsFatal": {
			reason: "An Input with no sources has nothing to merge.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"}
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"merge"},
		},
		"NoTargetGVKIsFatal": {
			reason: "The target needs a group version kind before anything else can be decided.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"name": "merged", "namespace": "ephemeral"}
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"target"},
		},
		"DuplicateSourceNamesIsFatal": {
			reason: "Two sources sharing a name would silently collapse into one requirement and one merge slot.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}},
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-2", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{`"a"`},
		},
		"TargetKindWithDotIsFatal": {
			reason: "A dot in target.kind collides with desiredName's group separator, which would break the derived key's injectivity.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "Config.Map", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"target.kind", "Config.Map"},
		},
		"TargetKindWithSlashIsFatal": {
			reason: "A slash in target.kind collides with desiredName's namespace/name separator, which would break the derived key's injectivity.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "Config/Map", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"target.kind", "Config/Map"},
		},
		"SourceKindWithDotIsFatal": {
			reason: "A dot or slash is never valid in a Kubernetes Kind; reject it on a source too.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "Config.Map", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{mentionsSourceA, "ref.kind"},
		},
		"TargetAPIVersionWithTwoSlashesIsFatal": {
			reason: "schema.FromAPIVersionAndKind silently drops both group and version on a malformed apiVersion; validate() must catch it before that happens.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1/beta1/gamma1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, nil)},
			want:     want{rsp: fatal()},
			mentions: []string{"target.apiVersion"},
		},
		"SecretTargetWritesBase64Data": {
			reason: "Server side apply never persists stringData, so a Secret target must write data with values base64 encoded.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "Secret", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{"a": items(sourceA)})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta:         &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: oneSourceRequirements(),
					Conditions:   mergedCondition("1 of 1 sources merged"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						"secret/ephemeral/merged": {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "Secret",
								"metadata": {"name": "merged", "namespace": "ephemeral"},
								"data": {"shared": "ZnJvbS1h", "only-a": "MQ=="}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"SecretSourceIntoSecretTargetRoundTrips": {
			reason: "A Secret source's data is already base64; decoding on read and re-encoding on write must round trip, not double encode.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "Secret", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "Secret", "name": "creds", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{"a": items(`{
				"apiVersion": "v1",
				"kind": "Secret",
				"metadata": {"name": "creds", "namespace": "platform"},
				"data": {"password": "aHVudGVyMg=="}
			}`)})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta: &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: &fnv1.Requirements{Resources: map[string]*fnv1.ResourceSelector{
						"a": {
							ApiVersion: "v1",
							Kind:       "Secret",
							Match:      &fnv1.ResourceSelector_MatchName{MatchName: "creds"},
							Namespace:  new("platform"),
						},
					}},
					Conditions: mergedCondition("1 of 1 sources merged"),
					Desired: &fnv1.State{Resources: map[string]*fnv1.Resource{
						"secret/ephemeral/merged": {
							Resource: resource.MustStructJSON(`{
								"apiVersion": "v1",
								"kind": "Secret",
								"metadata": {"name": "merged", "namespace": "ephemeral"},
								"data": {"password": "aHVudGVyMg=="}
							}`),
							Ready: fnv1.Ready_READY_TRUE,
						},
					}},
				},
			},
		},
		"SecretSourceInvalidBase64IsFatal": {
			reason: "A Secret source value that fails to decode must be Fatal, not silently treated as plaintext.",
			args: args{ctx: context.Background(), req: req(`{
				"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
				"kind": "Input",
				"target": {"apiVersion": "v1", "kind": "Secret", "name": "merged", "namespace": "ephemeral"},
				"sources": [
					{"name": "a", "ref": {"apiVersion": "v1", "kind": "Secret", "name": "creds", "namespace": "platform"}}
				]
			}`, map[string]*fnv1.Resources{"a": items(`{
				"apiVersion": "v1",
				"kind": "Secret",
				"metadata": {"name": "creds", "namespace": "platform"},
				"data": {"password": "not$$valid"}
			}`)})},
			want: want{
				rsp: &fnv1.RunFunctionResponse{
					Meta: &fnv1.ResponseMeta{Tag: testTag, Ttl: durationpb.New(0)},
					Requirements: &fnv1.Requirements{Resources: map[string]*fnv1.ResourceSelector{
						"a": {
							ApiVersion: "v1",
							Kind:       "Secret",
							Match:      &fnv1.ResourceSelector_MatchName{MatchName: "creds"},
							Namespace:  new("platform"),
						},
					}},
					Results: []*fnv1.Result{{
						Severity: fnv1.Severity_SEVERITY_FATAL,
						Target:   fnv1.Target_TARGET_COMPOSITE.Enum(),
					}},
				},
			},
			mentions: []string{mentionsSourceA, `"password"`},
		},
		"MissingRequiredResourcesCapabilityIsFatal": {
			reason: "Crossplane advertises capabilities but not required resources, which this function needs to resolve sources.",
			args: args{ctx: context.Background(), req: &fnv1.RunFunctionRequest{
				Meta:  &fnv1.RequestMeta{Tag: testTag, Capabilities: []fnv1.Capability{fnv1.Capability_CAPABILITY_CAPABILITIES}},
				Input: resource.MustStructJSON(oneSourceInput),
			}},
			want:     want{rsp: fatal()},
			mentions: []string{"required resources"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := &Function{log: logging.NewNopLogger()}
			rsp, err := f.RunFunction(tc.args.ctx, tc.args.req)

			opts := []cmp.Option{protocmp.Transform()}
			if len(tc.mentions) > 0 {
				// The wording of a fatal message is not part of the contract.
				// Severity, target, requirements and desired state still are.
				opts = append(opts, protocmp.IgnoreFields(&fnv1.Result{}, "message"))
			}
			if diff := cmp.Diff(tc.want.rsp, rsp, opts...); diff != "" {
				t.Errorf("%s\nf.RunFunction(...): -want rsp, +got rsp:\n%s", tc.reason, diff)
			}

			for _, m := range tc.mentions {
				if !resultsMention(rsp, m) {
					t.Errorf("%s\nno result mentions %q; got %v", tc.reason, m, rsp.GetResults())
				}
			}

			if diff := cmp.Diff(tc.want.err, err, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("%s\nf.RunFunction(...): -want err, +got err:\n%s", tc.reason, diff)
			}
		})
	}
}

const clusterScopedInput = `{
	"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
	"kind": "Input",
	"target": {"apiVersion": "apiextensions.crossplane.io/v1beta1", "kind": "EnvironmentConfig", "name": "merged"},
	"sources": [
		{"name": "a", "ref": {"apiVersion": "apiextensions.crossplane.io/v1beta1", "kind": "EnvironmentConfig", "name": "env-1"}}
	]
}`

func clusterScopedRequirements() *fnv1.Requirements {
	return &fnv1.Requirements{Resources: map[string]*fnv1.ResourceSelector{
		"a": {
			ApiVersion: "apiextensions.crossplane.io/v1beta1",
			Kind:       "EnvironmentConfig",
			Match:      &fnv1.ResourceSelector_MatchName{MatchName: "env-1"},
		},
	}}
}

func resultsMention(rsp *fnv1.RunFunctionResponse, substr string) bool {
	for _, r := range rsp.GetResults() {
		if strings.Contains(r.GetMessage(), substr) {
			return true
		}
	}
	return false
}

// fatalResultMentions reports whether rsp carries a Fatal result whose message
// contains substr. resultsMention alone cannot tell a Fatal from a Warning
// with the same wording, which is exactly the reversal a mutation of
// response.Fatal to response.Warning would produce.
func fatalResultMentions(rsp *fnv1.RunFunctionResponse, substr string) bool {
	for _, r := range rsp.GetResults() {
		if r.GetSeverity() == fnv1.Severity_SEVERITY_FATAL && strings.Contains(r.GetMessage(), substr) {
			return true
		}
	}
	return false
}

// TestDesiredNameIsInjective covers every way two distinct target identities
// could be folded onto one desired state key. A collision would let two
// pipeline steps silently share one entry, which defeats the per-step
// independence the design claims.
func TestDesiredNameIsInjective(t *testing.T) {
	identities := []struct {
		gk        schema.GroupKind
		namespace string
		name      string
	}{
		// A name is an RFC 1123 subdomain and may contain dots, so a dot
		// separator would fold these two together.
		{schema.GroupKind{Kind: kindConfigMap}, "", "ephemeral.merged"},
		{schema.GroupKind{Kind: kindConfigMap}, nsEphemeral, nameMerged},

		// A namespace boundary that a dot separator would also lose.
		{schema.GroupKind{Kind: kindConfigMap}, nsEphemeral, "app.merged"},
		{schema.GroupKind{Kind: kindConfigMap}, "ephemeral.app", nameMerged},

		// Same kind, same namespace, same name, different API group. A key
		// built from the kind alone would fold these together with the core
		// group entry above.
		{schema.GroupKind{Group: "example.org", Kind: kindConfigMap}, nsEphemeral, nameMerged},
		{schema.GroupKind{Group: "other.example.org", Kind: kindConfigMap}, nsEphemeral, nameMerged},

		// The group is dot separated from the kind, so a group boundary must
		// not be confusable with a kind that ends in something group-like.
		{schema.GroupKind{Group: "org", Kind: kindConfigMap}, nsEphemeral, nameMerged},
		{schema.GroupKind{Group: "example.org", Kind: kindConfigMap}, "", nameMerged},

		// A group that looks like a namespace must not be confusable with one,
		// which is what keeps the dot and the slash from bleeding into each
		// other.
		{schema.GroupKind{Group: nsEphemeral, Kind: kindConfigMap}, "", nameMerged},

		{schema.GroupKind{Group: "apiextensions.crossplane.io", Kind: "EnvironmentConfig"}, "", nameMerged},
		{schema.GroupKind{Kind: "Secret"}, nsEphemeral, nameMerged},
	}

	seen := make(map[resource.Name]string, len(identities))
	for _, id := range identities {
		desc := id.gk.String() + "|" + id.namespace + "|" + id.name
		key := desiredName(id.gk, id.namespace, id.name)

		// The raw apiVersion must never reach the key: it already contains a
		// slash, which would reintroduce the ambiguity the separator fixes.
		if strings.Contains(string(key), "/v1") {
			t.Errorf("desiredName(%s) = %q, which embeds an API version", desc, key)
		}

		if other, collided := seen[key]; collided {
			t.Errorf("desiredName(%s) = %q, which collides with %s", desc, key, other)
			continue
		}
		seen[key] = desc
	}
}

// TestDesiredNameIgnoresTheAPIVersion is the other half of injectivity: one
// group and kind at two versions is one object, so bumping target.apiVersion
// must not change the key. If it did, the bump would delete the target and
// create a new one rather than read the same object through a newer version.
func TestDesiredNameIgnoresTheAPIVersion(t *testing.T) {
	const input = `{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "apiextensions.crossplane.io/%s", "kind": "EnvironmentConfig", "name": "merged"},
		"sources": [
			{"name": "a", "ref": {"apiVersion": "apiextensions.crossplane.io/%s", "kind": "EnvironmentConfig", "name": "env-1"}}
		]
	}`

	f := &Function{log: logging.NewNopLogger()}

	got := make([]string, 0, 2)
	for _, version := range []string{"v1beta1", "v1"} {
		rsp, err := f.RunFunction(context.Background(), req(
			fmt.Sprintf(input, version, version),
			map[string]*fnv1.Resources{"a": items(fmt.Sprintf(`{
				"apiVersion": "apiextensions.crossplane.io/%s",
				"kind": "EnvironmentConfig",
				"metadata": {"name": "env-1"},
				"data": {"region": "eu-west-1"}
			}`, version))},
		))
		if err != nil {
			t.Fatalf("f.RunFunction(%s): %v", version, err)
		}
		if len(rsp.GetResults()) != 0 {
			t.Fatalf("f.RunFunction(%s): unexpected results: %v", version, rsp.GetResults())
		}
		got = append(got, keys(rsp)...)
	}

	want := []string{envConfigKey, envConfigKey}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the desired key changed with the API version: -want, +got:\n%s", diff)
	}
}

// TestRequirementsAreStableAcrossCalls covers the invariant Crossplane uses to
// decide whether a function has converged. Requirements that change between the
// unresolved and resolved calls produce "requirements didn't stabilize".
func TestRequirementsAreStableAcrossCalls(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	first, err := f.RunFunction(context.Background(), req(twoSourceInput, nil))
	if err != nil {
		t.Fatalf("f.RunFunction(unresolved): %v", err)
	}

	second, err := f.RunFunction(context.Background(), req(twoSourceInput, map[string]*fnv1.Resources{
		"a": items(sourceA),
		"b": items(sourceB),
	}))
	if err != nil {
		t.Fatalf("f.RunFunction(resolved): %v", err)
	}

	if !proto.Equal(first.GetRequirements(), second.GetRequirements()) {
		t.Errorf("requirements changed between calls:\n%s",
			cmp.Diff(first.GetRequirements(), second.GetRequirements(), protocmp.Transform()))
	}
}

// TestRenameDropsTheOldDesiredEntry covers the load-bearing behaviour of the
// derived key. The key follows target.name, so renaming leaves the old entry
// out of desired state, which is what makes Crossplane delete the old object.
func TestRenameDropsTheOldDesiredEntry(t *testing.T) {
	const before = `{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "old", "namespace": "ephemeral"},
		"sources": [
			{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
		]
	}`

	f := &Function{log: logging.NewNopLogger()}

	first, err := f.RunFunction(context.Background(), req(before, map[string]*fnv1.Resources{"a": items(sourceA)}))
	if err != nil {
		t.Fatalf("f.RunFunction(before rename): %v", err)
	}
	if _, ok := first.GetDesired().GetResources()["configmap/ephemeral/old"]; !ok {
		t.Fatalf("before the rename, want key %q, got %v", "configmap/ephemeral/old", keys(first))
	}

	// After the rename Crossplane still observes the old composed resource
	// under its old key. The function must not carry it into desired state.
	after := req(oneSourceInput, map[string]*fnv1.Resources{"a": items(sourceA)})
	after.Observed = &fnv1.State{Resources: map[string]*fnv1.Resource{
		"configmap/ephemeral/old": {
			Resource: resource.MustStructJSON(`{
				"apiVersion": "v1",
				"kind": "ConfigMap",
				"metadata": {"name": "old", "namespace": "ephemeral"},
				"data": {"shared": "from-a"}
			}`),
		},
	}}

	second, err := f.RunFunction(context.Background(), after)
	if err != nil {
		t.Fatalf("f.RunFunction(after rename): %v", err)
	}
	if _, ok := second.GetDesired().GetResources()["configmap/ephemeral/old"]; ok {
		t.Errorf("after the rename the old key survives in desired state: %v", keys(second))
	}
	if _, ok := second.GetDesired().GetResources()[mergedKey]; !ok {
		t.Errorf("after the rename, want key %q, got %v", mergedKey, keys(second))
	}
}

func keys(rsp *fnv1.RunFunctionResponse) []string {
	out := make([]string, 0, len(rsp.GetDesired().GetResources()))
	for k := range rsp.GetDesired().GetResources() {
		out = append(out, k)
	}
	return out
}

// TestEmbeddedBlobsDeepMerge asserts the merged blob's content, not its
// formatting. Key order and indentation are a property of the YAML encoder.
func TestEmbeddedBlobsDeepMerge(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rsp, err := f.RunFunction(context.Background(), req(`{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"parseEmbedded": true,
		"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
		"sources": [
			{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}},
			{"name": "b", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-2", "namespace": "platform"}}
		]
	}`, map[string]*fnv1.Resources{
		"a": items(`{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {"name": "map-1", "namespace": "platform"},
			"data": {"app.yaml": "shared: from-a\nonlyA: 1\n"}
		}`),
		"b": items(`{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {"name": "map-2", "namespace": "platform"},
			"data": {"app.yaml": "shared: from-b\nonlyB: 2\n"}
		}`),
	}))
	if err != nil {
		t.Fatalf("f.RunFunction(...): %v", err)
	}
	if len(rsp.GetResults()) != 0 {
		t.Fatalf("unexpected results: %v", rsp.GetResults())
	}

	cd, ok := rsp.GetDesired().GetResources()[mergedKey]
	if !ok {
		t.Fatalf("want key %q, got %v", mergedKey, keys(rsp))
	}

	blob := cd.GetResource().AsMap()["data"].(map[string]any)["app.yaml"].(string)

	got := map[string]any{}
	if err := yaml.Unmarshal([]byte(blob), &got); err != nil {
		t.Fatalf("cannot decode the merged blob %q: %v", blob, err)
	}

	// Keys from both sources survive, and the later source wins the overlap.
	wantBlob := map[string]any{"shared": "from-b", "onlyA": 1, "onlyB": 2}
	if diff := cmp.Diff(wantBlob, got); diff != "" {
		t.Errorf("merged app.yaml: -want, +got:\n%s", diff)
	}
}

// TestSingleSourceExplicitNullKeySurvives covers the accumulator's seed step:
// a lone source is normalized, not merged onto the empty accumulator. Routing
// it through Merge instead would silently drop this key under MergeObjects
// and MergeObjectsAppendArrays, since mergo treats an explicit nil the same
// as an absent key. The target is EnvironmentConfig, not ConfigMap, because a
// ConfigMap target lowers data to strings and a nil has no string form.
func TestSingleSourceExplicitNullKeySurvives(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rsp, err := f.RunFunction(context.Background(), req(`{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"mergeStrategy": "MergeObjects",
		"target": {"apiVersion": "apiextensions.crossplane.io/v1beta1", "kind": "EnvironmentConfig", "name": "merged"},
		"sources": [
			{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
		]
	}`, map[string]*fnv1.Resources{
		"a": items(`{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {"name": "map-1", "namespace": "platform"},
			"data": {"nullKey": null, "other": "x"}
		}`),
	}))
	if err != nil {
		t.Fatalf("f.RunFunction(...): %v", err)
	}
	if len(rsp.GetResults()) != 0 {
		t.Fatalf("unexpected results: %v", rsp.GetResults())
	}

	cd, ok := rsp.GetDesired().GetResources()[envConfigKey]
	if !ok {
		t.Fatalf("want key %q, got %v", envConfigKey, keys(rsp))
	}

	data := cd.GetResource().AsMap()["data"].(map[string]any)
	v, ok := data["nullKey"]
	if !ok {
		t.Fatalf("composed data %v is missing nullKey", data)
	}
	if v != nil {
		t.Errorf("composed data[\"nullKey\"] = %v, want nil", v)
	}
}

// TestRunsAreDeterministic covers the map ordering hazard: the requirement map
// must never drive the merge, because the merge is an order dependent left fold.
func TestRunsAreDeterministic(t *testing.T) {
	// A fresh request per call, not one request reused across iterations:
	// response.To aliases req.Desired, and RunFunction writes its output back
	// through that alias, so a reused request would let one call's output
	// mutate the next call's input instead of each run starting clean.
	newReq := func() *fnv1.RunFunctionRequest {
		return req(`{
			"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
			"kind": "Input",
			"parseEmbedded": true,
			"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
			"sources": [
				{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}},
				{"name": "b", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-2", "namespace": "platform"}},
				{"name": "c", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-3", "namespace": "platform"}}
			]
		}`, map[string]*fnv1.Resources{
			"a": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-1"}, "data": {"app.yaml": "shared: a\nfromA: 1\n"}}`),
			"b": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-2"}, "data": {"app.yaml": "shared: b\nfromB: 2\n"}}`),
			"c": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-3"}, "data": {"app.yaml": "shared: c\nfromC: 3\n"}}`),
		})
	}

	f := &Function{log: logging.NewNopLogger()}

	var first []byte
	for i := range 20 {
		rsp, err := f.RunFunction(context.Background(), newReq())
		if err != nil {
			t.Fatalf("run %d: f.RunFunction(...): %v", i, err)
		}
		if len(rsp.GetResults()) != 0 {
			t.Fatalf("run %d: unexpected results: %v", i, rsp.GetResults())
		}

		b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(rsp.GetDesired())
		if err != nil {
			t.Fatalf("run %d: cannot marshal desired state: %v", i, err)
		}

		if i == 0 {
			first = b
			continue
		}
		if !bytes.Equal(first, b) {
			t.Fatalf("run %d: desired state is not byte identical to run 0", i)
		}
	}
}

// TestSetsMergedConditionNamingSkippedOptionalSources covers the reason the
// optional-source summary is a condition and not a result: conditions are
// level triggered, so this must not become event spam on every reconcile.
func TestSetsMergedConditionNamingSkippedOptionalSources(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rsp, err := f.RunFunction(context.Background(), req(`{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
		"sources": [
			{"name": "a", "resolution": "Optional", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}},
			{"name": "b", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-2", "namespace": "platform"}}
		]
	}`, map[string]*fnv1.Resources{
		"a": items(),
		"b": items(sourceB),
	}))
	if err != nil {
		t.Fatalf("f.RunFunction(...): %v", err)
	}

	for _, r := range rsp.GetResults() {
		if r.GetSeverity() == fnv1.Severity_SEVERITY_NORMAL || r.GetSeverity() == fnv1.Severity_SEVERITY_WARNING {
			t.Errorf("skipping an optional source must not produce a Normal or Warning result: %v", r)
		}
	}

	var found bool
	for _, c := range rsp.GetConditions() {
		if c.GetType() != mergedType {
			continue
		}
		found = true
		if !strings.Contains(c.GetMessage(), "skipped optional: a") {
			t.Errorf("Merged condition message %q does not name the skipped source %q", c.GetMessage(), "a")
		}
	}
	if !found {
		t.Fatalf("no Merged condition in %v", rsp.GetConditions())
	}
}

// TestMergedConditionDistinguishesAbsentResourceFromAbsentField proves the two
// clauses are separate and ordered: skipped optional (the resource does not
// exist) before no data (the resource exists but its field does not),
// matching mergedMessage's fixed clause order.
func TestMergedConditionDistinguishesAbsentResourceFromAbsentField(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rsp, err := f.RunFunction(context.Background(), req(`{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "ephemeral"},
		"sources": [
			{"name": "a", "resolution": "Optional", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}},
			{"name": "b", "resolution": "Optional", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-2", "namespace": "platform"}},
			{"name": "c", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-3", "namespace": "platform"}}
		]
	}`, map[string]*fnv1.Resources{
		"a": items(),
		"b": items(`{"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "map-2", "namespace": "platform"}}`),
		"c": items(sourceA),
	}))
	if err != nil {
		t.Fatalf("f.RunFunction(...): %v", err)
	}

	var msg string
	var found bool
	for _, c := range rsp.GetConditions() {
		if c.GetType() != mergedType {
			continue
		}
		found = true
		msg = c.GetMessage()
	}
	if !found {
		t.Fatalf("no Merged condition in %v", rsp.GetConditions())
	}

	skippedAt := strings.Index(msg, "skipped optional: a")
	noDataAt := strings.Index(msg, "no data: b")
	if skippedAt == -1 || noDataAt == -1 {
		t.Fatalf("Merged condition message %q does not name both an absent resource and an absent field, distinctly", msg)
	}
	if skippedAt > noDataAt {
		t.Errorf("Merged condition message %q orders \"no data\" before \"skipped optional\", want the reverse", msg)
	}
	if want := "1 of 3 sources merged"; !strings.HasPrefix(msg, want) {
		t.Errorf("Merged condition message %q does not start with %q; only source %q actually contributed", msg, want, "c")
	}
}

// TestObservedCompositeIsReadBeforeRequirements pins the ordering the
// confinement guard depends on. The guard needs the XR's namespace while it is
// still building requirements, so a missing composite must surface before any
// requirement is returned.
func TestObservedCompositeIsReadBeforeRequirements(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	r := req(oneSourceInput, nil)
	r.Observed = &fnv1.State{Composite: &fnv1.Resource{
		Resource: resource.MustStructJSON(`{
			"apiVersion": "example.org/v1",
			"kind": "XMerger",
			"metadata": {"name": "merger-xr"}
		}`),
	}}

	rsp, err := f.RunFunction(context.Background(), r)
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	if rsp.GetRequirements() == nil {
		t.Fatal("RunFunction() returned no requirements for a cluster scoped composite")
	}
	if got := len(rsp.GetRequirements().GetResources()); got != 1 {
		t.Errorf("requirements = %d resources, want 1", got)
	}
}

// TestNamespacedCompositeComposesWithoutANamespace is the core of Release 2.
// Leaving the composed resource's namespace empty is what arms Crossplane's own
// IsObjectNamespaced check, which is gated on that field being empty. Setting it
// lets a cluster scoped target be created and then permanently orphaned.
func TestNamespacedCompositeComposesWithoutANamespace(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	r := req(nsXRInput, map[string]*fnv1.Resources{"a": items(sourceATeamA)})
	r.Observed = &fnv1.State{Composite: &fnv1.Resource{
		Resource: resource.MustStructJSON(`{
			"apiVersion": "example.org/v1",
			"kind": "XMerger",
			"metadata": {"name": "merger-xr", "namespace": "team-a"}
		}`),
	}}

	rsp, err := f.RunFunction(context.Background(), r)
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}

	desired := rsp.GetDesired().GetResources()
	if len(desired) != 1 {
		t.Fatalf("desired = %d resources, want 1", len(desired))
	}

	// The key uses the XR's namespace, because that is where Crossplane will
	// put the object.
	const wantKey = "configmap/team-a/merged"
	cd, ok := desired[wantKey]
	if !ok {
		t.Fatalf("desired keys = %v, want %q", keys(rsp), wantKey)
	}

	// The resource itself must carry no namespace.
	fields := cd.GetResource().GetFields()
	meta, ok := fields["metadata"]
	if !ok {
		t.Fatal("composed resource has no metadata")
	}
	if ns, ok := meta.GetStructValue().GetFields()["namespace"]; ok {
		t.Errorf("composed resource namespace = %q, want it unset", ns.GetStringValue())
	}
}

// TestNamespacedTargetNamespaceFieldPathResolvingToEmptyIsNotFatal is the
// namespaced half of NamespaceFieldPathResolvingToEmptyIsFatal. Under a
// namespaced composite the resolved value is discarded in favour of the XR's
// own namespace, so an empty resolution is harmless there, unlike under a
// cluster scoped composite where it would silently drop the namespace.
func TestNamespacedTargetNamespaceFieldPathResolvingToEmptyIsNotFatal(t *testing.T) {
	r := &fnv1.RunFunctionRequest{
		Meta: &fnv1.RequestMeta{Tag: testTag},
		Observed: &fnv1.State{Composite: &fnv1.Resource{
			Resource: resource.MustStructJSON(`{
				"apiVersion": "example.org/v1",
				"kind": "XMerger",
				"metadata": {"name": "merger-xr", "namespace": "team-a"},
				"spec": {"targetNamespace": ""}
			}`),
		}},
		Input: resource.MustStructJSON(`{
			"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
			"kind": "Input",
			"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespaceFromCompositeFieldPath": "spec.targetNamespace"},
			"sources": [
				{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "team-a"}}
			]
		}`),
		RequiredResources: map[string]*fnv1.Resources{
			"a": items(sourceATeamA),
		},
	}

	f := &Function{log: logging.NewNopLogger()}
	rsp, err := f.RunFunction(context.Background(), r)
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	for _, res := range rsp.GetResults() {
		if res.GetSeverity() == fnv1.Severity_SEVERITY_FATAL {
			t.Fatalf("an empty namespaceFromCompositeFieldPath resolution must not fatal under a namespaced composite: %v", rsp.GetResults())
		}
	}

	const wantKey = "configmap/team-a/merged"
	if _, ok := rsp.GetDesired().GetResources()[wantKey]; !ok {
		t.Errorf("desired keys = %v, want %q", keys(rsp), wantKey)
	}
}

// TestNamespacedCompositeIgnoresTargetNamespace covers a ConfigMap target whose
// target.namespace disagrees with the XR. Crossplane clamps it either way, so
// the function warns rather than failing.
func TestNamespacedCompositeIgnoresTargetNamespace(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	r := req(nsXRInput, map[string]*fnv1.Resources{"a": items(sourceATeamA)})
	r.Observed = &fnv1.State{Composite: &fnv1.Resource{
		Resource: resource.MustStructJSON(`{
			"apiVersion": "example.org/v1",
			"kind": "XMerger",
			"metadata": {"name": "merger-xr", "namespace": "team-a"}
		}`),
	}}

	rsp, err := f.RunFunction(context.Background(), r)
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	if !resultsMention(rsp, "is ignored") {
		t.Errorf("results did not warn about the ignored target.namespace: %v", rsp.GetResults())
	}

	// The Warning result is transient; the Merged condition is the persisted
	// surface, so the same note must also land there.
	var found bool
	for _, c := range rsp.GetConditions() {
		if c.GetType() != mergedType {
			continue
		}
		found = true
		const want = `ignored target.namespace: "ephemeral", using "team-a"`
		if !strings.Contains(c.GetMessage(), want) {
			t.Errorf("Merged condition message %q does not record %q", c.GetMessage(), want)
		}
	}
	if !found {
		t.Fatalf("no Merged condition in %v", rsp.GetConditions())
	}
}

// crossNsInput reads a source from "platform" while the composite lives
// elsewhere. The target namespace matches the XR so only the source is at issue.
const crossNsInput = `{
	"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
	"kind": "Input",
	"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "team-a"},
	"sources": [
		{"name": "a", "ref": {"apiVersion": "v1", "kind": "ConfigMap", "name": "map-1", "namespace": "platform"}}
	]
}`

func namespacedXR(input string, required map[string]*fnv1.Resources) *fnv1.RunFunctionRequest {
	r := req(input, required)
	r.Observed = &fnv1.State{Composite: &fnv1.Resource{
		Resource: resource.MustStructJSON(`{
			"apiVersion": "example.org/v1",
			"kind": "XMerger",
			"metadata": {"name": "merger-xr", "namespace": "team-a"}
		}`),
	}}
	return r
}

// TestCrossNamespaceSourceIsRejectedAndNotRequested is the security case. The
// selector must be absent from the requirements, not merely unmerged: a
// returned selector is a fetch Crossplane will perform.
func TestCrossNamespaceSourceIsRejectedAndNotRequested(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rsp, err := f.RunFunction(context.Background(), namespacedXR(crossNsInput, nil))
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	if !resultsMention(rsp, "allowCrossNamespace") {
		t.Errorf("results did not name the remedy: %v", rsp.GetResults())
	}
	if _, ok := rsp.GetRequirements().GetResources()["a"]; ok {
		t.Error("the disallowed selector was returned; Crossplane would fetch it")
	}
}

// TestCrossNamespaceSourceNeedsBothControls proves the flag alone is not
// enough. A boolean cannot pin which namespace is trusted.
func TestCrossNamespaceSourceNeedsBothControls(t *testing.T) {
	flagOnly := strings.Replace(crossNsInput,
		`"namespace": "platform"}}`,
		`"namespace": "platform"}, "allowCrossNamespace": true}`, 1)

	f := &Function{log: logging.NewNopLogger()}
	rsp, err := f.RunFunction(context.Background(), namespacedXR(flagOnly, nil))
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	if !resultsMention(rsp, "allowedSourceNamespaces") {
		t.Errorf("results did not name the missing allow list: %v", rsp.GetResults())
	}
	if _, ok := rsp.GetRequirements().GetResources()["a"]; ok {
		t.Error("the selector was returned despite the namespace not being listed")
	}
}

// TestCrossNamespaceSourceAllowedWhenBothSet is the permitted path.
func TestCrossNamespaceSourceAllowedWhenBothSet(t *testing.T) {
	allowed := strings.Replace(crossNsInput,
		`"namespace": "platform"}}`,
		`"namespace": "platform"}, "allowCrossNamespace": true}`, 1)
	allowed = strings.Replace(allowed,
		`"sources": [`,
		`"allowedSourceNamespaces": ["platform"], "sources": [`, 1)

	f := &Function{log: logging.NewNopLogger()}
	rsp, err := f.RunFunction(context.Background(), namespacedXR(allowed, nil))
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	if _, ok := rsp.GetRequirements().GetResources()["a"]; !ok {
		t.Fatalf("the permitted selector was not returned: %v", rsp.GetRequirements())
	}
}

// TestMergedConditionRecordsPermittedCrossNamespaceRead is the audit trail: a
// permitted cross-namespace read must be recorded on the Merged condition, not
// pass silently, so an operator can see which Compositions reach outside their
// namespace.
func TestMergedConditionRecordsPermittedCrossNamespaceRead(t *testing.T) {
	allowed := strings.Replace(crossNsInput,
		`"namespace": "platform"}}`,
		`"namespace": "platform"}, "allowCrossNamespace": true}`, 1)
	allowed = strings.Replace(allowed,
		`"sources": [`,
		`"allowedSourceNamespaces": ["platform"], "sources": [`, 1)

	f := &Function{log: logging.NewNopLogger()}
	rsp, err := f.RunFunction(context.Background(), namespacedXR(allowed, map[string]*fnv1.Resources{
		"a": items(sourceA),
	}))
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}

	var found bool
	for _, c := range rsp.GetConditions() {
		if c.GetType() != mergedType {
			continue
		}
		found = true
		// Both the source's Composition-local name and the real object it read
		// must appear; naming only the local alias is not an audit trail.
		const want = "cross-namespace: a -> ConfigMap/map-1 in platform"
		if !strings.Contains(c.GetMessage(), want) {
			t.Errorf("Merged condition message %q does not record the permitted cross-namespace read %q", c.GetMessage(), want)
		}
	}
	if !found {
		t.Fatalf("no Merged condition in %v", rsp.GetConditions())
	}
}

// TestClusterScopedCompositeIsUnconfined pins that Release 1 behaviour is
// untouched. There is no observed composite namespace here.
func TestClusterScopedCompositeIsUnconfined(t *testing.T) {
	f := &Function{log: logging.NewNopLogger()}

	rsp, err := f.RunFunction(context.Background(), req(crossNsInput, nil))
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	if _, ok := rsp.GetRequirements().GetResources()["a"]; !ok {
		t.Error("a cluster scoped composite must not be confined")
	}
}

// TestAllowCrossNamespaceOnClusterScopedKindWarns covers the no-op case. An
// empty ref.namespace already means a cluster scoped kind, so the flag does
// nothing and the author should be told.
func TestAllowCrossNamespaceOnClusterScopedKindWarns(t *testing.T) {
	const input = `{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "v1", "kind": "ConfigMap", "name": "merged", "namespace": "team-a"},
		"sources": [
			{"name": "a", "allowCrossNamespace": true, "ref": {"apiVersion": "apiextensions.crossplane.io/v1beta1", "kind": "EnvironmentConfig", "name": "env-1"}}
		]
	}`

	f := &Function{log: logging.NewNopLogger()}
	rsp, err := f.RunFunction(context.Background(), namespacedXR(input, nil))
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	if !resultsMention(rsp, "has no effect") {
		t.Errorf("results did not warn about the no-op flag: %v", rsp.GetResults())
	}
	if _, ok := rsp.GetRequirements().GetResources()["a"]; !ok {
		t.Error("a cluster scoped source must still be requested")
	}
}

// TestNamespacedCompositeRejectsClusterScopedTarget fails fast rather than
// letting Crossplane reject it several reconciles later.
func TestNamespacedCompositeRejectsClusterScopedTarget(t *testing.T) {
	const input = `{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "apiextensions.crossplane.io/v1beta1", "kind": "EnvironmentConfig", "name": "merged"},
		"sources": [
			{"name": "a", "ref": {"apiVersion": "apiextensions.crossplane.io/v1beta1", "kind": "EnvironmentConfig", "name": "env-1"}}
		]
	}`

	f := &Function{log: logging.NewNopLogger()}
	rsp, err := f.RunFunction(context.Background(), namespacedXR(input, nil))
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	if !fatalResultMentions(rsp, "cluster scoped") {
		t.Errorf("no fatal result rejecting the cluster scoped target: %v", rsp.GetResults())
	}
	if got := len(rsp.GetDesired().GetResources()); got != 0 {
		t.Errorf("desired resources = %d, want 0 for a rejected cluster scoped target", got)
	}
}

// TestClusterScopedTargetMatchesOnGroupKind proves the check is not kind only.
// A user's own namespaced EnvironmentConfig in another group is legitimate and
// must not be rejected.
func TestClusterScopedTargetMatchesOnGroupKind(t *testing.T) {
	if isClusterScopedTarget(schema.GroupKind{Group: "mygroup.io", Kind: "EnvironmentConfig"}) {
		t.Error("mygroup.io/EnvironmentConfig was treated as cluster scoped")
	}
	if !isClusterScopedTarget(schema.GroupKind{Group: "apiextensions.crossplane.io", Kind: "EnvironmentConfig"}) {
		t.Error("the Crossplane EnvironmentConfig was not treated as cluster scoped")
	}
}

// TestNamespacedCompositeRejectsMisdirectedSecret. Relocating a Secret the
// author placed elsewhere moves secret material into the namespace the tenant
// controls, so this is fatal where a ConfigMap only warns.
func TestNamespacedCompositeRejectsMisdirectedSecret(t *testing.T) {
	const input = `{
		"apiVersion": "resources-merger.fn.canilho.net/v1alpha2",
		"kind": "Input",
		"target": {"apiVersion": "v1", "kind": "Secret", "name": "merged", "namespace": "vault-secrets"},
		"sources": [
			{"name": "a", "ref": {"apiVersion": "v1", "kind": "Secret", "name": "src", "namespace": "team-a"}}
		]
	}`

	f := &Function{log: logging.NewNopLogger()}
	rsp, err := f.RunFunction(context.Background(), namespacedXR(input, nil))
	if err != nil {
		t.Fatalf("RunFunction() error = %v, want nil", err)
	}
	if !fatalResultMentions(rsp, "relocate") {
		t.Errorf("no fatal result rejecting the misdirected Secret: %v", rsp.GetResults())
	}
	if got := len(rsp.GetDesired().GetResources()); got != 0 {
		t.Errorf("desired resources = %d, want 0 for a rejected misdirected Secret", got)
	}
}
