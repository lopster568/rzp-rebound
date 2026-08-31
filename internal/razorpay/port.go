package razorpay

import (
	"context"
	"errors"
	"time"
)

// Order status values.
const (
	OrderStatusCreated   = "created"
	OrderStatusAttempted = "attempted"
	OrderStatusPaid      = "paid"
)

// Payment status values.
const (
	PaymentStatusCreated    = "created"
	PaymentStatusAuthorized = "authorized"
	PaymentStatusCaptured   = "captured"
	PaymentStatusFailed     = "failed"
)

// Payment link status values.
const (
	PaymentLinkStatusCreated = "created"
	PaymentLinkStatusPaid    = "paid"
)

// Notification media a payment link can be resent over.
const (
	MediumSMS   = "sms"
	MediumEmail = "email"
)

// ErrorSourcePendingFixture and ErrorStepPendingFixture are what the fake
// reports in error.source and error.step. The real per-code values are not
// documented in testdata, and guessing them would put a made-up fact in the
// repository. Phase 1 captures a live test-mode failure and replaces these.
const (
	ErrorSourcePendingFixture = "pending_fixture_source"
	ErrorStepPendingFixture   = "pending_fixture_step"
)

// Errors the port returns.
var (
	ErrOrderNotFound       = errors.New("razorpay: order not found")
	ErrPaymentNotFound     = errors.New("razorpay: payment not found")
	ErrOrderAlreadyPaid    = errors.New("razorpay: order is already paid")
	ErrPaymentLinkNotFound = errors.New("razorpay: payment link not found")
	ErrUnknownCard         = errors.New("razorpay: card is not in the test-card table")
	ErrUnsupportedMedium   = errors.New("razorpay: medium is not sms or email")
	ErrAmountNotPositive   = errors.New("razorpay: amount must be positive")
)

// Order is a Razorpay order. Amounts are in paise.
type Order struct {
	ID          string            `json:"id"`
	AmountPaise int64             `json:"amount"`
	AmountPaid  int64             `json:"amount_paid"`
	AmountDue   int64             `json:"amount_due"`
	Currency    string            `json:"currency"`
	Receipt     string            `json:"receipt"`
	Status      string            `json:"status"`
	Attempts    int               `json:"attempts"`
	CreatedAt   int64             `json:"created_at"`
	Notes       map[string]string `json:"notes,omitempty"`
}

// Payment is a Razorpay payment. Amounts are in paise. The five error fields
// are populated only when Status is failed.
type Payment struct {
	ID               string `json:"id"`
	OrderID          string `json:"order_id"`
	AmountPaise      int64  `json:"amount"`
	Currency         string `json:"currency"`
	Status           string `json:"status"`
	Method           string `json:"method"`
	CreatedAt        int64  `json:"created_at"`
	ErrorCode        string `json:"error_code,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
	ErrorSource      string `json:"error_source,omitempty"`
	ErrorStep        string `json:"error_step,omitempty"`
	ErrorReason      string `json:"error_reason,omitempty"`
}

// PaymentLink is a Razorpay payment link.
//
// The field set here has not been checked against a live response. Phase 1
// captures a fixture and corrects it.
type PaymentLink struct {
	ID          string `json:"id"`
	ShortURL    string `json:"short_url"`
	Status      string `json:"status"`
	AmountPaise int64  `json:"amount"`
	Currency    string `json:"currency"`
	ReferenceID string `json:"reference_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// NotifyReceipt records that a resend call returned success. It says nothing
// about whether a person read anything.
type NotifyReceipt struct {
	LinkID string
	Medium string
	// Accepted reports that the notification API call succeeded.
	Accepted    bool
	RequestedAt time.Time
}

// CreateOrderRequest is the input to CreateOrder. AmountPaise is in paise.
type CreateOrderRequest struct {
	AmountPaise int64
	Currency    string
	Receipt     string
	Notes       map[string]string
}

// CreatePaymentLinkRequest is the input to CreatePaymentLink.
//
// The request body this maps to has not been checked against the live API.
// Phase 1 captures a fixture and corrects it.
type CreatePaymentLinkRequest struct {
	AmountPaise int64
	Currency    string
	Description string
	ReferenceID string
	NotifySMS   bool
	NotifyEmail bool
}

// Port is the Razorpay surface this project uses. The fake, the live client,
// and the replay client all satisfy it, and contract_test.go runs the same
// assertions against each.
type Port interface {
	CreateOrder(ctx context.Context, req CreateOrderRequest) (Order, error)
	FetchOrder(ctx context.Context, orderID string) (Order, error)
	ListPaymentsForOrder(ctx context.Context, orderID string) ([]Payment, error)
	FetchPayment(ctx context.Context, paymentID string) (Payment, error)
	CreatePaymentLink(ctx context.Context, req CreatePaymentLinkRequest) (PaymentLink, error)
	ResendPaymentLinkNotification(ctx context.Context, linkID, medium string) (NotifyReceipt, error)
}
