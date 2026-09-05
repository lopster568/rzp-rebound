package mcpserver

// The tool input and output types.
//
// Every output type here is built from a riskitem.RiskItem and from what the
// Intervener reported about an action. Two things are deliberately absent from
// all of them.
//
// A customer's email address and phone number never appear. A summary says
// there is a channel and which media it supports, which is everything a model
// needs in order to choose an action, and the address itself is something only
// the intervention engine ever handles. Nothing on this wire can leak what
// nothing on this wire holds.
//
// A failure class never appears either. internal/classify's vocabulary is
// built around whether the same instrument can be presented again, and this
// engine cannot present anything again: showing a model a field that says
// "transient_retry_eligible" would be advertising an action that does not
// exist. The class is still computed and still handed to the policy, which is
// the one reader that acts on it. See docs/INDIA-CONSTRAINTS-AUDIT.md.

// ListRiskItemsInput is empty. The tool takes no arguments: an invocation is
// given its items and cannot ask for others.
type ListRiskItemsInput struct{}

// RiskItemSummary is one item as the agent first sees it: enough to choose
// what to look at, not enough to act.
type RiskItemSummary struct {
	ItemID string `json:"item_id"`
	// Source is which detector found the debt: failed_payment, unpaid_order,
	// or overdue_invoice.
	Source string `json:"source"`
	// AmountDuePaise is what is still outstanding, which is the number an
	// action is about. AmountPaise is the whole debt, carried alongside it
	// because a partial payment makes the two disagree and the difference is
	// worth seeing.
	AmountDuePaise int64  `json:"amount_due_paise"`
	AmountPaise    int64  `json:"amount_paise"`
	Currency       string `json:"currency"`
	// AgingDays is whole days since the debt started being at risk.
	AgingDays int `json:"aging_days"`
	// HasContact reports that there is somewhere to send a message. It never
	// says where.
	HasContact bool `json:"has_contact"`
	// HandleKind is what the customer can already pay against: payment_link,
	// invoice, or empty when there is nothing yet.
	HandleKind string `json:"handle_kind"`
}

// ListRiskItemsOutput is the queue.
type ListRiskItemsOutput struct {
	Items []RiskItemSummary `json:"items"`
	Note  string            `json:"note"`
}

// GetRiskItemInput names one item.
type GetRiskItemInput struct {
	ItemID string `json:"item_id" jsonschema:"the risk item id, exactly as list_risk_items returned it"`
}

// RiskItemDetail is one item in full, with the evidence the detector saw.
type RiskItemDetail struct {
	ItemID string `json:"item_id"`
	Source string `json:"source"`
	// SourceID is the Razorpay resource the detector read: a payment, an
	// order, or an invoice id.
	SourceID string `json:"source_id"`
	// RootOrderID is the order the debt sits on, and the key two detectors
	// collapse on when they find the same debt.
	RootOrderID     string `json:"root_order_id,omitempty"`
	AmountPaise     int64  `json:"amount_paise"`
	AmountPaidPaise int64  `json:"amount_paid_paise"`
	AmountDuePaise  int64  `json:"amount_due_paise"`
	Currency        string `json:"currency"`
	AtRiskSince     int64  `json:"at_risk_since"`
	AgingDays       int    `json:"aging_days"`
	HasContact      bool   `json:"has_contact"`
	// Channels is which media a notification could use, as "email" and "sms".
	// It is derived from whether an address exists and it never carries one.
	Channels   []string `json:"channels"`
	HandleKind string   `json:"handle_kind"`
	HandleID   string   `json:"handle_id,omitempty"`
	HandleURL  string   `json:"handle_url,omitempty"`
	// Signal is the optional evidence behind the sighting. A field a source
	// does not have is absent: an empty failure_reason means no failure was
	// observed, not that a failure had no reason.
	Signal RiskSignal `json:"signal"`
	Note   string     `json:"note"`
}

// RiskSignal is riskitem.Signal on the wire.
//
// EmailStatus and SmsStatus are what Razorpay reported about an invoice
// notification. They say a send was accepted. Neither says a person read
// anything, and no field here ever will.
type RiskSignal struct {
	FailureCode   string `json:"failure_code,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	FailureSource string `json:"failure_source,omitempty"`
	FailureStep   string `json:"failure_step,omitempty"`
	Method        string `json:"method,omitempty"`
	Attempts      int    `json:"attempts"`
	EmailStatus   string `json:"email_status,omitempty"`
	SmsStatus     string `json:"sms_status,omitempty"`
}

// RecordDecisionInput is the decision the agent has to state before it acts.
type RecordDecisionInput struct {
	ItemID    string `json:"item_id" jsonschema:"the risk item this decision is about"`
	Action    string `json:"action" jsonschema:"one of notify_email, notify_sms, create_payment_link, resend_link, log_promise, escalate, cancel_write_off, do_nothing"`
	Reasoning string `json:"reasoning" jsonschema:"why this action and not another, in your own words. It goes in the audit trail a compliance reviewer reads."`
}

// RecordDecisionOutput confirms the decision is on the record.
type RecordDecisionOutput struct {
	Recorded bool   `json:"recorded"`
	ItemID   string `json:"item_id"`
	Action   string `json:"action"`
	Note     string `json:"note"`
}

// NotifyItemInput asks Razorpay to tell the customer about the handle the item
// already has.
type NotifyItemInput struct {
	ItemID string `json:"item_id" jsonschema:"the risk item to notify about"`
	Medium string `json:"medium,omitempty" jsonschema:"email or sms. Defaults to email."`
}

// CreatePaymentLinkForItemInput raises a link for an item that has nothing to
// pay against yet.
type CreatePaymentLinkForItemInput struct {
	ItemID string `json:"item_id" jsonschema:"the risk item the link is for"`
}

// ResendLinkForItemInput sends the handle the item already carries again.
//
// It names no link id: the item carries the handle, and a link this server has
// never seen is not one it can act on. It names no medium either, because the
// lawful action set has one resend action and it does not carry one: a resend
// goes out the way the handle's first notification did.
type ResendLinkForItemInput struct {
	ItemID string `json:"item_id" jsonschema:"the risk item whose existing link should be sent again"`
}

// LogPromiseInput records that a customer said they will pay.
type LogPromiseInput struct {
	ItemID string `json:"item_id" jsonschema:"the risk item the promise is about"`
	// PromisedAt is a date rather than a timestamp because a promise to pay is
	// made in days.
	PromisedAt string `json:"promised_at" jsonschema:"the date the customer said they would pay, as YYYY-MM-DD or RFC3339"`
	DaysHold   int    `json:"days_hold,omitempty" jsonschema:"how many days to leave this item alone. Zero records the promise with no hold."`
	Note       string `json:"note" jsonschema:"what the customer actually said, in one sentence. A compliance reviewer reads this row."`
}

// EscalateItemInput hands an item to a person.
type EscalateItemInput struct {
	ItemID string `json:"item_id" jsonschema:"the risk item to hand over"`
	Reason string `json:"reason" jsonschema:"what a person needs to look at, in one sentence"`
}

// ActionOutput is what every action tool returns, allowed or refused.
//
// A refusal is a successful tool call carrying the verdict, not a transport
// error. The model has to be able to read why it was refused, and a protocol
// error is not something it can reason about.
//
// Allowed and Accepted are two different facts and the gap between them is the
// point. Allowed says the gate and the policy let the action through.
// Accepted says the call the action makes came back clean. Neither says a
// person received anything, and Observable is the strongest thing that was
// actually seen, as field:value.
type ActionOutput struct {
	ItemID        string `json:"item_id"`
	Action        string `json:"action"`
	Allowed       bool   `json:"allowed"`
	PolicyVerdict string `json:"policy_verdict"`
	PolicyRule    string `json:"policy_rule"`
	PolicyReason  string `json:"policy_reason"`
	// Remaining is what the policy said this item has left under its own
	// counting rule, carried so a refusal and a near miss look different.
	Remaining  int    `json:"remaining"`
	Accepted   bool   `json:"accepted"`
	Observable string `json:"observable,omitempty"`
	HandleKind string `json:"handle_kind,omitempty"`
	HandleID   string `json:"handle_id,omitempty"`
	HandleURL  string `json:"handle_url,omitempty"`
	// Error is why the action did not happen, when it did not. It is the
	// applier's refusal reason or the failure of the call it made.
	Error string `json:"error,omitempty"`
	Note  string `json:"note,omitempty"`
}
