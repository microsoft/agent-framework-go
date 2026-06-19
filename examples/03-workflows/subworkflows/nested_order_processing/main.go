// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"
	"reflect"

	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/workflow"
	"github.com/microsoft/agent-framework-go/workflow/inproc"
)

var _ = demo.NewLogger(
	"Nested Sub-Workflows",
	"This sample composes an order-processing workflow from nested reusable subworkflows.",
)

type OrderInfo struct {
	OrderID              string
	Amount               int
	PaymentTransactionID string
	TrackingNumber       string
	Carrier              string
}

type FraudRiskAssessedEvent struct {
	RiskScore int
}

func (e FraudRiskAssessedEvent) Data() any {
	return e.RiskScore
}

const fraudStateScope = "FraudAnalysis"

func main() {
	fraudCheckWorkflow := buildFraudCheckWorkflow()
	paymentWorkflow := buildPaymentWorkflow(fraudCheckWorkflow)
	shippingWorkflow := buildShippingWorkflow()
	orderWorkflow := buildOrderProcessingWorkflow(paymentWorkflow, shippingWorkflow)

	ctx := context.Background()
	orderID := "ORD-001"
	demo.Assistantf("Starting order processing for %q", orderID)

	run, err := inproc.Default.RunStreaming(ctx, orderWorkflow, orderID)
	if err != nil {
		demo.Panic(err)
	}
	defer func() { _ = run.Close(ctx) }()

	for evt, err := range run.WatchStream(ctx) {
		if err != nil {
			demo.Panic(err)
		}
		switch event := evt.(type) {
		case FraudRiskAssessedEvent:
			demo.Assistantf("[Event from nested subworkflow] Risk score: %d/100", event.RiskScore)
		case workflow.OutputEvent:
			demo.Assistantf("Order completed: %v", event.Output)
		case workflow.ErrorEvent:
			demo.Panic(event.Error)
		case workflow.ExecutorFailedEvent:
			demo.Panicf("executor %q failed: %v", event.ExecutorID, event.Error)
		}
	}
}

func buildFraudCheckWorkflow() *workflow.Workflow {
	analyzePatterns := workflow.NewExecutor("AnalyzePatterns", func(ctx *workflow.Context, order OrderInfo) (OrderInfo, error) {
		demo.Assistantf("    [Payment/FraudCheck/AnalyzePatterns] Analyzing %s", order.OrderID)
		patternsFound := 2
		if err := ctx.QueueStateUpdate("patternsFound", fraudStateScope, patternsFound); err != nil {
			return OrderInfo{}, err
		}
		demo.Assistantf("    [Payment/FraudCheck/AnalyzePatterns] Found %d suspicious patterns", patternsFound)
		return order, nil
	}).Bind()

	calculateRisk := workflow.NewExecutor("CalculateRiskScore", func(ctx *workflow.Context, order OrderInfo) (OrderInfo, error) {
		demo.Assistantf("    [Payment/FraudCheck/CalculateRiskScore] Calculating risk for %s", order.OrderID)
		value, err := ctx.ReadState("patternsFound", fraudStateScope)
		if err != nil {
			return OrderInfo{}, err
		}
		patternsFound, ok := value.(int)
		if !ok {
			return OrderInfo{}, fmt.Errorf("patternsFound state has type %T, want int", value)
		}
		riskScore := patternsFound*20 + 13
		if err := ctx.AddEvent(FraudRiskAssessedEvent{RiskScore: riskScore}); err != nil {
			return OrderInfo{}, err
		}
		demo.Assistantf("    [Payment/FraudCheck/CalculateRiskScore] Risk score: %d/100", riskScore)
		return order, nil
	}).Bind()

	wf, err := workflow.NewBuilder(analyzePatterns).
		AddEdge(analyzePatterns, calculateRisk).
		WithOutputFrom(calculateRisk).
		Build()
	if err != nil {
		demo.Panic(err)
	}
	return wf
}

func buildPaymentWorkflow(fraudCheckWorkflow *workflow.Workflow) *workflow.Workflow {
	validatePayment := workflow.NewExecutor("ValidatePayment", func(order OrderInfo) OrderInfo {
		demo.Assistantf("  [Payment/ValidatePayment] Validated payment for %s ($%.2f)", order.OrderID, float64(order.Amount)/100)
		return order
	}).Bind()
	fraudCheck := inproc.BindSubworkflowAsExecutor(fraudCheckWorkflow, "FraudCheck")
	chargePayment := workflow.NewExecutor("ChargePayment", func(order OrderInfo) OrderInfo {
		order.PaymentTransactionID = "TXN-ORD-001"
		demo.Assistantf("  [Payment/ChargePayment] Payment processed: %s", order.PaymentTransactionID)
		return order
	}).Bind()

	wf, err := workflow.NewBuilder(validatePayment).
		AddEdge(validatePayment, fraudCheck).
		AddEdge(fraudCheck, chargePayment).
		WithOutputFrom(chargePayment).
		Build()
	if err != nil {
		demo.Panic(err)
	}
	return wf
}

func buildShippingWorkflow() *workflow.Workflow {
	selectCarrier := selectCarrierExecutor()
	createShipment := workflow.NewExecutor("CreateShipment", func(order OrderInfo) OrderInfo {
		order.TrackingNumber = "TRACK-ORD-001"
		demo.Assistantf("  [Shipping/CreateShipment] Shipment created: %s", order.TrackingNumber)
		return order
	}).Bind()

	wf, err := workflow.NewBuilder(selectCarrier).
		AddEdge(selectCarrier, createShipment).
		WithOutputFrom(createShipment).
		Build()
	if err != nil {
		demo.Panic(err)
	}
	return wf
}

func buildOrderProcessingWorkflow(paymentWorkflow *workflow.Workflow, shippingWorkflow *workflow.Workflow) *workflow.Workflow {
	orderReceived := workflow.NewExecutor("OrderReceived", func(orderID string) OrderInfo {
		demo.Assistantf("[OrderReceived] Processing order %q", orderID)
		return OrderInfo{OrderID: orderID, Amount: 9999}
	}).Bind()
	payment := inproc.BindSubworkflowAsExecutor(paymentWorkflow, "Payment")
	shipping := inproc.BindSubworkflowAsExecutor(shippingWorkflow, "Shipping")
	orderCompleted := workflow.NewExecutor("OrderCompleted", func(order OrderInfo) string {
		demo.Assistantf("[OrderCompleted] Payment: %s", order.PaymentTransactionID)
		demo.Assistantf("[OrderCompleted] Shipping: %s - %s", order.Carrier, order.TrackingNumber)
		return "Order " + order.OrderID + " completed. Tracking: " + order.TrackingNumber
	}).Bind()

	wf, err := workflow.NewBuilder(orderReceived).
		AddEdge(orderReceived, payment).
		AddEdge(payment, shipping).
		AddEdge(shipping, orderCompleted).
		WithOutputFrom(orderCompleted).
		Build()
	if err != nil {
		demo.Panic(err)
	}
	return wf
}

func selectCarrierExecutor() workflow.ExecutorBinding {
	binding := workflow.NewExecutor("SelectCarrier", func(ctx *workflow.Context, order OrderInfo) error {
		order.Carrier = "Express"
		demo.Assistantf("  [Shipping/SelectCarrier] Selected carrier: %s", order.Carrier)
		return ctx.SendMessage("", order)
	}).Extend(&workflow.Executor{
		ConfigureProtocol: func(pb *workflow.ProtocolBuilder) (*workflow.ProtocolBuilder, error) {
			pb.SendsMessageType(reflect.TypeFor[OrderInfo]())
			return pb, nil
		},
	}).Bind()
	return binding
}
