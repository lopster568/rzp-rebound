package mcpserver

// The tool input and output types.
//
// Every output type here is built from batch.AgentVisibleOrder and from state
// read back out of the gateway. None of them has a field that exists on
// batch.Order, which is the type that holds the answer key.
// TestToolResponseNeverContainsGroundTruthFields walks them by reflection and
// walks the marshaled responses for the values as well, because the phase 2
// review found a leak that was carried by an ordering rather than by a field
// name.

// ListFailedPaymentsInput is empty. The tool takes no arguments: an invocation
// is given its orders and cannot ask for others.
type ListFailedPaymentsInput struct{}

// FailedPaymentSummary is one order as the agent sees it. Four fields, the
// same four as batch.AgentVisibleOrder, which is a type that never held the
// ground truth.
type FailedPaymentSummary struct {
	OrderID     string `json:"order_id"`
	AmountPaise int64  `json:"amount_paise"`
	Currency    string `json:"currency"`
	Receipt     string `json:"receipt"`
}

// ListFailedPaymentsOutput is the order list.
type ListFailedPaymentsOutput struct {
	Orders []FailedPaymentSummary `json:"orders"`
	Note   string                 `json:"note"`
}

// GetPaymentDetailInput names one order.
type GetPaymentDetailInput struct {
	OrderID string `json:"order_id" jsonschema:"the order id, exactly as list_failed_payments returned it"`
}

// PaymentDetail is one order's state, read out of the gateway.
//
// FailureClass is internal/classify's reading of the error the gateway
// returned. It is a component of this system doing its job on observable
// input, not a peek at the manifest, and the rules arm gets the same reading
// from the same function. An arm that could not see it would be a different
// arm rather than a different decision-maker.
type PaymentDetail struct {
	OrderID       string `json:"order_id"`
	AmountPaise   int64  `json:"amount_paise"`
	Currency      string `json:"currency"`
	Receipt       string `json:"receipt"`
	OrderStatus   string `json:"order_status"`
	AmountPaid    int64  `json:"amount_paid_paise"`
	AttemptsSeen  int    `json:"attempts_seen"`
	FailureCode   string `json:"failure_code"`
	FailureReason string `json:"failure_reason"`
	FailureSource string `json:"failure_source"`
	FailureStep   string `json:"failure_step"`
	FailureClass  string `json:"failure_class"`
	Note          string `json:"note"`
}

// RecordDecisionInput is the decision the agent has to state before it acts.
type RecordDecisionInput struct {
	OrderID   string `json:"order_id" jsonschema:"the order this decision is about"`
	Action    string `json:"action" jsonschema:"one of retry_same_instrument, request_reauth, request_new_instrument, escalate, do_nothing"`
	Reasoning string `json:"reasoning" jsonschema:"why this action and not another, in your own words. It goes in the audit trail a compliance reviewer reads."`
}

// RecordDecisionOutput confirms the decision is on the record.
type RecordDecisionOutput struct {
	Recorded bool   `json:"recorded"`
	OrderID  string `json:"order_id"`
	Action   string `json:"action"`
	Note     string `json:"note"`
}

// RetryPaymentInput names the order to re-present the instrument on.
type RetryPaymentInput struct {
	OrderID string `json:"order_id" jsonschema:"the order to attempt again"`
}

// CreatePaymentLinkInput raises a link for a customer to come back through.
type CreatePaymentLinkInput struct {
	OrderID string `json:"order_id" jsonschema:"the order the link is for"`
	Purpose string `json:"purpose,omitempty" jsonschema:"reauth when the customer has to authenticate again, new_instrument when the card cannot work. Defaults to reauth."`
}

// ResendNotificationInput asks Razorpay to send an existing link again.
type ResendNotificationInput struct {
	OrderID       string `json:"order_id" jsonschema:"the order the link belongs to"`
	PaymentLinkID string `json:"payment_link_id" jsonschema:"the link id create_payment_link returned"`
	Medium        string `json:"medium,omitempty" jsonschema:"email or sms. Defaults to email."`
}

// EscalateToHumanInput hands an order to a person.
type EscalateToHumanInput struct {
	OrderID string `json:"order_id" jsonschema:"the order to hand over"`
	Reason  string `json:"reason" jsonschema:"what a person needs to look at, in one sentence"`
}

// ActionOutput is what every action tool returns, allowed or refused.
//
// A refusal is a successful tool call carrying the verdict, not a transport
// error. The model has to be able to read why it was refused, and a protocol
// error is not something it can reason about.
type ActionOutput struct {
	OrderID           string `json:"order_id"`
	Action            string `json:"action"`
	Allowed           bool   `json:"allowed"`
	PolicyVerdict     string `json:"policy_verdict"`
	PolicyRule        string `json:"policy_rule"`
	PolicyReason      string `json:"policy_reason"`
	AttemptsRemaining int    `json:"attempts_remaining"`
	PaymentID         string `json:"payment_id,omitempty"`
	PaymentLinkID     string `json:"payment_link_id,omitempty"`
	PaymentLinkURL    string `json:"payment_link_url,omitempty"`
	// NotificationNote is notify.Receipt.AuditPhrase. It says that the
	// notification API call succeeded and it does not say that a person
	// received or read anything, because nothing here observes that.
	NotificationNote string `json:"notification_note,omitempty"`
	OrderStatus      string `json:"order_status,omitempty"`
	Note             string `json:"note,omitempty"`
}
