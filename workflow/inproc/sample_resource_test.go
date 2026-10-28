// Copyright (c) Microsoft. All rights reserved.

package inproc_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

type sampleUserRequest struct {
	RequestType, Type        string
	Amount                   int
	ID, Priority, PolicyType string
}
type sampleResourceRequest struct {
	ID, ResourceType string
	Amount           int
	Priority         string
}
type samplePolicyRequest struct {
	ID, ResourceType string
	Amount           int
	PolicyType       string
}
type sampleResourceResponse struct {
	ID, ResourceType string
	Allocated        int
	Source           string
}
type samplePolicyResponse struct {
	ID       string
	Approved bool
	Reason   string
}
type sampleRequestFinished struct {
	ID, RequestType string
	Resource        *sampleResourceResponse
	Policy          *samplePolicyResponse
}

func sampleFactory(id string, configure func(*workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error)) workflow.ExecutorBinding {
	binding := workflow.BindNewExecutorFunc(id, func(_ string, id string) (*workflow.Executor, error) {
		return &workflow.Executor{ID: id, ConfigureProtocol: configure}, nil
	})
	binding.SupportsConcurrentSharedExecution = true
	return binding
}

func sampleRequestOf[T any](value any) bool {
	request, ok := value.(*workflow.ExternalRequest)
	return ok && request.Data.Is(reflect.TypeFor[T]())
}

func sampleResponseOf[T any](value any) bool {
	response, ok := value.(*workflow.ExternalResponse)
	return ok && response.Data.Is(reflect.TypeFor[T]())
}

func sampleResourceChild(t *testing.T) *workflow.Workflow {
	t.Helper()
	resourcePort := workflow.RequestPort{ID: "ResourceRequestor.Requests[ResourceRequest=>ResourceResponse]", Request: reflect.TypeFor[sampleResourceRequest](), Response: reflect.TypeFor[sampleResourceResponse]()}
	policyPort := workflow.RequestPort{ID: "ResourceRequestor.Requests[PolicyCheckRequest=>PolicyResponse]", Request: reflect.TypeFor[samplePolicyRequest](), Response: reflect.TypeFor[samplePolicyResponse]()}
	requestor := sampleFactory("ResourceRequestor", func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
		pb.SendsMessageType(reflect.TypeFor[sampleResourceRequest](), reflect.TypeFor[samplePolicyRequest]())
		pb.YieldsOutputType(reflect.TypeFor[sampleRequestFinished]())
		request := func(ctx *workflow.Context, requests []sampleUserRequest) error {
			for _, request := range requests {
				var value any
				switch request.RequestType {
				case "resource":
					value = sampleResourceRequest{ID: request.ID, ResourceType: request.Type, Amount: request.Amount, Priority: request.Priority}
				case "policy":
					value = samplePolicyRequest{ID: request.ID, ResourceType: request.Type, Amount: request.Amount, PolicyType: request.PolicyType}
				default:
					return fmt.Errorf("unknown request type %q", request.RequestType)
				}
				if err := ctx.SendMessage("", value); err != nil {
					return err
				}
			}
			return nil
		}
		pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[sampleUserRequest](), nil, func(ctx *workflow.Context, value any) (any, error) {
			return nil, request(ctx, []sampleUserRequest{value.(sampleUserRequest)})
		})
		pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[[]sampleUserRequest](), nil, func(ctx *workflow.Context, value any) (any, error) {
			return nil, request(ctx, value.([]sampleUserRequest))
		})
		pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[sampleResourceResponse](), nil, func(ctx *workflow.Context, value any) (any, error) {
			response := value.(sampleResourceResponse)
			return nil, ctx.YieldOutput(sampleRequestFinished{ID: response.ID, RequestType: "resource", Resource: new(response)})
		})
		pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[samplePolicyResponse](), nil, func(ctx *workflow.Context, value any) (any, error) {
			response := value.(samplePolicyResponse)
			return nil, ctx.YieldOutput(sampleRequestFinished{ID: response.ID, RequestType: "policy", Policy: new(response)})
		})
		return pb, nil
	})
	resource, policy := resourcePort.Bind(), policyPort.Bind()
	wf, err := workflow.NewBuilder(requestor).
		AddEdge(requestor, resource, workflow.WithEdgeCondition(func(value any) bool { _, ok := value.(sampleResourceRequest); return ok })).
		AddEdge(resource, requestor, workflow.WithEdgeCondition(func(value any) bool { _, ok := value.(sampleResourceResponse); return ok })).
		AddEdge(requestor, policy, workflow.WithEdgeCondition(func(value any) bool { _, ok := value.(samplePolicyRequest); return ok })).
		AddEdge(policy, requestor, workflow.WithEdgeCondition(func(value any) bool { _, ok := value.(samplePolicyResponse); return ok })).
		WithOutputFrom(requestor).Build()
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func sampleResourceGate(id string, policy bool) workflow.ExecutorBinding {
	return sampleFactory(id, func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
		cache := &workflow.StatefulExecutorCache[map[string]int]{StateKey: "State", InitialStateFactory: func() map[string]int {
			if policy {
				return map[string]int{"cpu": 5, "memory": 20, "disk": 1000}
			}
			return map[string]int{"cpu": 10, "memory": 50, "disk": 100}
		}}
		pb.SendsMessageType(reflect.TypeFor[*workflow.ExternalRequest](), reflect.TypeFor[*workflow.ExternalResponse]())
		pb.RouteBuilder.AddCatchAll(func(ctx *workflow.Context, portable workflow.PortableValue) (any, error) {
			switch value := portable.Any().(type) {
			case *workflow.ExternalResponse:
				return nil, ctx.SendMessage("", value)
			case *workflow.ExternalRequest:
				var response any
				err := cache.InvokeWithState(ctx, false, func(_ *workflow.Context, state map[string]int) (map[string]int, error) {
					if policy {
						request, ok := workflow.PortableValueAs[samplePolicyRequest](value.Data)
						if !ok {
							return state, fmt.Errorf("unexpected policy request data")
						}
						if quota, ok := state[request.ResourceType]; request.PolicyType == "quota" && ok && request.Amount <= quota {
							response = samplePolicyResponse{ID: request.ID, Approved: true, Reason: fmt.Sprintf("Within quota (%d)", quota)}
						}
					} else {
						request, ok := workflow.PortableValueAs[sampleResourceRequest](value.Data)
						if !ok {
							return state, fmt.Errorf("unexpected resource request data")
						}
						if available, ok := state[request.ResourceType]; ok && available >= request.Amount {
							state[request.ResourceType] -= request.Amount
							response = sampleResourceResponse{ID: request.ID, ResourceType: request.ResourceType, Allocated: request.Amount, Source: "cache"}
						}
					}
					return state, nil
				})
				if err != nil {
					return nil, err
				}
				if response == nil {
					return nil, ctx.SendMessage("", value)
				}
				wrapped, err := value.CreateResponse(response)
				if err != nil {
					return nil, err
				}
				return nil, ctx.SendMessage("", wrapped)
			default:
				return nil, fmt.Errorf("unexpected gate input %T", value)
			}
		})
		return pb, nil
	})
}

func sampleResourceWorkflow(t *testing.T) *workflow.Workflow {
	t.Helper()
	coordinator := sampleFactory("Coordinator", func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
		pb.SendsMessageType(reflect.TypeFor[sampleUserRequest]())
		pb.YieldsOutputType(reflect.TypeFor[sampleRequestFinished]())
		pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[[]sampleUserRequest](), nil, func(ctx *workflow.Context, value any) (any, error) {
			state, err := ctx.ReadState("StateKey", "")
			if err != nil {
				return nil, err
			}
			count, _ := state.(int)
			requests := value.([]sampleUserRequest)
			for _, request := range requests {
				if err := ctx.SendMessage("", request); err != nil {
					return nil, err
				}
			}
			return nil, ctx.QueueStateUpdate("StateKey", "", count+len(requests))
		})
		pb.RouteBuilder.AddHandlerRaw(reflect.TypeFor[sampleRequestFinished](), nil, func(ctx *workflow.Context, value any) (any, error) {
			state, err := ctx.ReadState("StateKey", "")
			if err != nil {
				return nil, err
			}
			count, _ := state.(int)
			if err := ctx.YieldOutput(value); err != nil {
				return nil, err
			}
			return nil, ctx.QueueStateUpdate("StateKey", "", count-1)
		})
		return pb, nil
	})
	host := inproc.BindSubworkflowAsExecutor(sampleResourceChild(t), "ResourceWorkflow")
	cache, policy := sampleResourceGate("ResourceCache", false), sampleResourceGate("QuotaPolicyEngine", true)
	resourcePort := workflow.RequestPort{ID: "ResourceRequest", Request: reflect.TypeFor[sampleResourceRequest](), Response: reflect.TypeFor[sampleResourceResponse]()}
	policyPort := workflow.RequestPort{ID: "PolicyCheckRequest", Request: reflect.TypeFor[samplePolicyRequest](), Response: reflect.TypeFor[samplePolicyResponse]()}
	resource, policyBoundary := resourcePort.Bind(), policyPort.Bind()
	wf, err := workflow.NewBuilder(coordinator).AddChain(coordinator, []workflow.ExecutorBinding{host, coordinator}, true).
		AddEdge(host, cache, workflow.WithEdgeCondition(sampleRequestOf[sampleResourceRequest])).
		AddEdge(cache, resource, workflow.WithEdgeCondition(sampleRequestOf[sampleResourceRequest])).
		AddEdge(resource, cache, workflow.WithEdgeCondition(sampleResponseOf[sampleResourceResponse])).
		AddEdge(cache, host, workflow.WithEdgeCondition(sampleResponseOf[sampleResourceResponse])).
		AddEdge(host, policy, workflow.WithEdgeCondition(sampleRequestOf[samplePolicyRequest])).
		AddEdge(policy, policyBoundary, workflow.WithEdgeCondition(sampleRequestOf[samplePolicyRequest])).
		AddEdge(policyBoundary, policy, workflow.WithEdgeCondition(sampleResponseOf[samplePolicyResponse])).
		AddEdge(policy, host, workflow.WithEdgeCondition(sampleResponseOf[samplePolicyResponse])).
		WithOutputFrom(coordinator).Build()
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func runSampleResourceRequests(t *testing.T, environment *inproc.ExecutionEnvironment) {
	t.Helper()
	requests := []sampleUserRequest{
		{RequestType: "resource", Type: "cpu", Amount: 2, Priority: "normal", ID: "1"},
		{RequestType: "policy", Type: "cpu", Amount: 3, PolicyType: "quota", ID: "3"},
		{RequestType: "resource", Type: "memory", Amount: 15, Priority: "normal", ID: "2"},
		{RequestType: "policy", Type: "memory", Amount: 100, PolicyType: "quota", ID: "6"},
		{RequestType: "resource", Type: "gpu", Amount: 2, Priority: "high", ID: "5"},
		{RequestType: "policy", Type: "disk", Amount: 500, PolicyType: "quota", ID: "4"},
		{RequestType: "policy", Type: "cpu", Amount: 1, PolicyType: "security", ID: "7"},
	}
	firstExpected := []sampleRequestFinished{
		{ID: "1", RequestType: "resource", Resource: &sampleResourceResponse{ID: "1", ResourceType: "cpu", Allocated: 2, Source: "cache"}},
		{ID: "2", RequestType: "resource", Resource: &sampleResourceResponse{ID: "2", ResourceType: "memory", Allocated: 15, Source: "cache"}},
		{ID: "3", RequestType: "policy", Policy: &samplePolicyResponse{ID: "3", Approved: true, Reason: "Within quota (5)"}},
		{ID: "4", RequestType: "policy", Policy: &samplePolicyResponse{ID: "4", Approved: true, Reason: "Within quota (1000)"}},
	}
	secondExpected := []sampleRequestFinished{
		{ID: "5", RequestType: "resource", Resource: &sampleResourceResponse{ID: "5", ResourceType: "gpu", Allocated: 1, Source: "external"}},
		{ID: "6", RequestType: "policy", Policy: &samplePolicyResponse{ID: "6", Approved: false, Reason: "External Rejection"}},
		{ID: "7", RequestType: "policy", Policy: &samplePolicyResponse{ID: "7", Approved: true, Reason: "External Approval"}},
	}
	run, err := environment.Run(t.Context(), sampleResourceWorkflow(t), requests)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Close(t.Context()) }()
	if status, err := run.GetStatus(t.Context()); err != nil || status != inproc.RunStatusPendingRequests {
		t.Fatalf("first phase status = %v, %v", status, err)
	}
	events := slices.Collect(run.NewEvents())
	if hasErrorEvents(events) {
		t.Fatalf("first phase errors: %v", events)
	}
	var finished []sampleRequestFinished
	var resources, policies []*workflow.ExternalRequest
	for _, event := range events {
		switch event := event.(type) {
		case workflow.OutputEvent:
			if result, ok := event.Output.(sampleRequestFinished); ok {
				finished = append(finished, result)
			}
		case workflow.RequestInfoEvent:
			if event.Request.Data.Is(reflect.TypeFor[sampleResourceRequest]()) {
				resources = append(resources, event.Request)
			}
			if event.Request.Data.Is(reflect.TypeFor[samplePolicyRequest]()) {
				policies = append(policies, event.Request)
			}
		}
	}
	slices.SortFunc(finished, func(a, b sampleRequestFinished) int { return strings.Compare(a.ID, b.ID) })
	if !reflect.DeepEqual(finished, firstExpected) {
		t.Fatalf("local responses = %#v, want %#v", finished, firstExpected)
	}
	if len(resources) != 1 || len(policies) != 2 {
		t.Fatalf("external resource/policy requests = %d/%d, want 1/2", len(resources), len(policies))
	}
	var responses []any
	for _, request := range resources {
		data, ok := workflow.PortableValueAs[sampleResourceRequest](request.Data)
		if !ok || data.ID != "5" {
			t.Fatalf("unexpected resource request data: %#v", data)
		}
		response, err := request.CreateResponse(*secondExpected[0].Resource)
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	for _, request := range policies {
		data, ok := workflow.PortableValueAs[samplePolicyRequest](request.Data)
		if !ok || (data.ID != "6" && data.ID != "7") {
			t.Fatalf("unexpected policy request data: %#v", data)
		}
		index := 1
		if data.ID == "7" {
			index = 2
		}
		response, err := request.CreateResponse(*secondExpected[index].Policy)
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if _, err := run.Resume(t.Context(), responses...); err != nil {
		t.Fatal(err)
	}
	if status, err := run.GetStatus(t.Context()); err != nil || status != inproc.RunStatusIdle {
		t.Fatalf("resumed status = %v, %v", status, err)
	}
	events = slices.Collect(run.NewEvents())
	if hasErrorEvents(events) {
		t.Fatalf("resume errors: %v", events)
	}
	finished = nil
	for _, event := range events {
		if output, ok := event.(workflow.OutputEvent); ok {
			if result, ok := output.Output.(sampleRequestFinished); ok {
				finished = append(finished, result)
			}
		}
	}
	slices.SortFunc(finished, func(a, b sampleRequestFinished) int { return strings.Compare(a.ID, b.ID) })
	if !reflect.DeepEqual(finished, secondExpected) {
		t.Fatalf("external responses = %#v, want %#v", finished, secondExpected)
	}
}

func TestSample_Step9_SubworkflowRequestsWithLocalInterception(t *testing.T) {
	for _, environment := range sampleEnvironments() {
		t.Run(environment.name, func(t *testing.T) { runSampleResourceRequests(t, environment.env) })
	}
}

func TestSample_Step9_OffThreadMultiResponseResumeStatusIsStable(t *testing.T) {
	for i := range 50 {
		t.Run(fmt.Sprint(i), func(t *testing.T) { runSampleResourceRequests(t, inproc.OffThread) })
	}
}
