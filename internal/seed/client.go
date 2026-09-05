package seed

import (
	"context"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// Client is the slice of the Razorpay port a seed run needs: create a
// customer, create and issue an invoice, and create an order. It exists so
// ExecutePlan can be tested against a stub that makes no network call.
// Every method here already exists on *razorpay.Client (see
// internal/razorpay/invoices.go and internal/razorpay/client.go); this
// interface adds nothing to that surface, it only names the slice of it
// seeding uses. client_test.go asserts *razorpay.Client satisfies it.
type Client interface {
	CreateCustomer(ctx context.Context, req razorpay.CreateCustomerRequest) (razorpay.Customer, error)
	CreateInvoice(ctx context.Context, req razorpay.CreateInvoiceRequest) (razorpay.Invoice, error)
	IssueInvoice(ctx context.Context, invoiceID string) (razorpay.Invoice, error)
	CreateOrder(ctx context.Context, req razorpay.CreateOrderRequest) (razorpay.Order, error)
}
