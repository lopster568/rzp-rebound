package razorpay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// Error fields carried by a failed payment, observed against Razorpay test
// mode on 2026-08-31 and recorded in docs/RAZORPAY-TEST-MODE-NOTES.md. They
// replace the ErrorSourcePendingFixture and ErrorStepPendingFixture
// placeholders the offline half shipped, which stood in for exactly this.
//
// Every failed payment the 2026-08-31 walk produced carried all three, with no
// variation across the eight documented cards.
const (
	// ErrorClassBadRequest is the coarse class in error.code. Razorpay uses
	// it for a declined payment as well as for a malformed request, which is
	// why the classifier reads error.reason first.
	ErrorClassBadRequest = "BAD_REQUEST_ERROR"
	// ErrorSourceGateway is error.source on a payment the bank declined.
	ErrorSourceGateway = "gateway"
	// ErrorStepPaymentAuthorization is error.step on the same.
	ErrorStepPaymentAuthorization = "payment_authorization"
	// ReasonPaymentFailed is the only error.reason test mode produces for a
	// declined card. It names no cause, which is why it classifies as
	// unclassified rather than as a retry. DECISIONS.md has the reasoning.
	ReasonPaymentFailed = "payment_failed"
)

// DescriptionMissingResource is the error.description Razorpay returns for a
// resource that does not exist. It arrives with a 400 rather than a 404, which
// is why mapNotFound has to read it.
const DescriptionMissingResource = "The id provided does not exist"

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

// Notes is Razorpay's free-form notes map.
//
// It is a named type with its own decoder because Razorpay does not always
// send it as an object. An order created with notes comes back with a JSON
// object, and an order created without them comes back with an empty JSON
// array. Decoding straight into a map therefore failed the whole response for
// exactly the orders that had nothing interesting in this field, which is the
// worst place to fail: the order exists in Razorpay, the caller gets an error,
// and nothing in the caller knows the id of what it just created.
//
// Observed on 2026-08-31 by the live contract harness, minutes after that
// harness first ran. The fixture captures did not find it because every
// captured order was created with notes on it.
type Notes map[string]string

// UnmarshalJSON accepts an object, an empty array, or null.
//
// A non-empty array stays an error. It is not an empty map with a different
// spelling, and quietly dropping its contents would lose data that was on the
// order.
func (n *Notes) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*n = nil
		return nil
	}

	if trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return fmt.Errorf("razorpay: decode notes: %w", err)
		}
		if len(items) != 0 {
			return fmt.Errorf("razorpay: notes arrived as an array of %d item(s), which is not a map", len(items))
		}
		*n = Notes{}
		return nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return fmt.Errorf("razorpay: decode notes: %w", err)
	}

	// A note whose value is not a string keeps its JSON text rather than
	// failing the whole order. This project only ever writes string notes, but
	// an order created from the dashboard or by another integration can carry
	// a number or a boolean, and failing there would be the same defect this
	// type was added to fix: the order exists in Razorpay, the caller gets an
	// error, and nothing knows the id of what it just read. Review finding,
	// 2026-08-31.
	out := make(Notes, len(raw))
	for key, value := range raw {
		var str string
		if err := json.Unmarshal(value, &str); err == nil {
			out[key] = str
			continue
		}
		text := strings.TrimSpace(string(value))
		if text == "null" {
			text = ""
		}
		out[key] = text
	}
	*n = out
	return nil
}

// Order is a Razorpay order. Amounts are in paise.
type Order struct {
	ID          string `json:"id"`
	AmountPaise int64  `json:"amount"`
	AmountPaid  int64  `json:"amount_paid"`
	AmountDue   int64  `json:"amount_due"`
	Currency    string `json:"currency"`
	Receipt     string `json:"receipt"`
	Status      string `json:"status"`
	Attempts    int    `json:"attempts"`
	CreatedAt   int64  `json:"created_at"`
	Notes       Notes  `json:"notes,omitempty"`
}

// Payment is a Razorpay payment. Amounts are in paise. The five error fields
// are populated only when Status is failed.
type Payment struct {
	ID          string `json:"id"`
	OrderID     string `json:"order_id"`
	AmountPaise int64  `json:"amount"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	Method      string `json:"method"`
	// Email and Contact are the contact details the payer entered at
	// checkout, carried on the payment entity as "email" and "contact".
	//
	// They are the only contact a failed-payment sighting can honestly carry
	// that does not come out of a merchant-written order note: /v1/orders
	// answers with no email and no contact field at all. Both were on the
	// payment body captured on 2026-08-31 in
	// testdata/recorded/list_payments_after_failure.json, and both are
	// omitempty because a payment made through a flow that collected neither
	// carries neither, and an empty string there is the honest report rather
	// than a gap to fill in from somewhere else.
	Email            string `json:"email,omitempty"`
	Contact          string `json:"contact,omitempty"`
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
