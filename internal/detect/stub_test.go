package detect

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// errStub is what a stub returns when a test told it to fail. It is a
// sentinel so that a test can tell the failure it asked for from a decode
// error it did not.
var errStub = errors.New("detect_test: the stub was told to fail")

// stubGateway serves list pages out of decoded fixture bodies and records
// every call, so a test can assert on what a detector asked for as well as on
// what it built. It stands in for *razorpay.Client, which is why no detector
// holds a concrete client.
type stubGateway struct {
	orders   []razorpay.Order
	invoices []razorpay.Invoice
	payments map[string][]razorpay.Payment

	// Recorded calls, in the order they were made.
	orderOpts     []razorpay.ListOptions
	invoiceOpts   []razorpay.ListOptions
	paymentOrders []string

	// Failure injection. The counts are one based, so zero means never.
	orderErrOnCall    int
	invoiceErrOnCall  int
	paymentErrOnOrder string
}

func (s *stubGateway) ListOrders(_ context.Context, opts razorpay.ListOptions) ([]razorpay.Order, error) {
	s.orderOpts = append(s.orderOpts, opts)
	if s.orderErrOnCall > 0 && len(s.orderOpts) == s.orderErrOnCall {
		return nil, errStub
	}
	return pageOf(s.orders, opts), nil
}

func (s *stubGateway) ListInvoices(_ context.Context, opts razorpay.ListOptions) ([]razorpay.Invoice, error) {
	s.invoiceOpts = append(s.invoiceOpts, opts)
	if s.invoiceErrOnCall > 0 && len(s.invoiceOpts) == s.invoiceErrOnCall {
		return nil, errStub
	}
	return pageOf(s.invoices, opts), nil
}

func (s *stubGateway) ListPaymentsForOrder(_ context.Context, orderID string) ([]razorpay.Payment, error) {
	s.paymentOrders = append(s.paymentOrders, orderID)
	if s.paymentErrOnOrder != "" && orderID == s.paymentErrOnOrder {
		return nil, errStub
	}
	return s.payments[orderID], nil
}

// pageOf answers a list call the way Razorpay does: skip records, then return
// at most count of what is left, and return a short slice at the end.
func pageOf[T any](all []T, opts razorpay.ListOptions) []T {
	if opts.Skip >= len(all) {
		return []T{}
	}
	rest := all[opts.Skip:]
	if opts.Count > 0 && len(rest) > opts.Count {
		rest = rest[:opts.Count]
	}
	return append([]T(nil), rest...)
}

// The collection envelopes, redeclared here because razorpay keeps its own
// unexported. Redeclaring them is what lets every fixture below be a raw
// response body rather than a Go literal: the field names, the nulls, and the
// notes that arrive as an empty array all go through the same decoders the
// live client uses.
type (
	orderListBody struct {
		Count int              `json:"count"`
		Items []razorpay.Order `json:"items"`
	}
	invoiceListBody struct {
		Count int                `json:"count"`
		Items []razorpay.Invoice `json:"items"`
	}
	paymentListBody struct {
		Count int                `json:"count"`
		Items []razorpay.Payment `json:"items"`
	}
)

func decodeOrders(t *testing.T, raw string) []razorpay.Order {
	t.Helper()
	var body orderListBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode the order list fixture: %v", err)
	}
	return body.Items
}

func decodeInvoices(t *testing.T, raw string) []razorpay.Invoice {
	t.Helper()
	var body invoiceListBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode the invoice list fixture: %v", err)
	}
	return body.Items
}

func decodePayments(t *testing.T, raw string) []razorpay.Payment {
	t.Helper()
	var body paymentListBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode the payment list fixture: %v", err)
	}
	return body.Items
}
