// Package riskitem is the frozen contract for the unified revenue-at-risk
// engine: the one item type that three detectors produce, the policy gate
// reads, and the intervention engine acts on.
//
// This package is the WP0 contract. After the freeze commit, changes require
// the coordinator's sign-off. Every other work package compiles against the
// exported surface here, so a field renamed or a constant removed after the
// freeze breaks packages whose authors are not in the room.
//
// The package holds types and pure functions only. It talks to no network, no
// clock beyond what a caller hands it, and no Razorpay client, and it imports
// nothing outside the standard library. Detectors live in the packages that
// own their Razorpay calls; this package only says what they must return.
package riskitem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"
)

// Source says which detector produced an item. It is half of the identity of
// a RiskItem and it is not a display label: the strings are persisted in
// fixtures and in the audit trail, so they are part of the frozen contract.
type Source string

// The three detectors, and the only three.
const (
	// SourceFailedPayment is a payment Razorpay reports as failed. The debt is
	// the order behind it.
	SourceFailedPayment Source = "failed_payment"
	// SourceUnpaidOrder is an order that is created or attempted and not paid.
	// An order with attempts 0 was never tried, which is an abandonment rather
	// than a failure, and the Signal carries the difference.
	SourceUnpaidOrder Source = "unpaid_order"
	// SourceOverdueInvoice is an issued invoice that is not paid. Razorpay
	// mints an order for an invoice when it is issued, which is why an invoice
	// and an order can be the same debt. See RiskItem.RootOrderID.
	SourceOverdueInvoice Source = "overdue_invoice"
)

// Handle kinds a PayHandle can carry.
const (
	// HandleKindNone means there is nothing for a customer to pay against yet.
	// An intervention that wants a link has to create one.
	HandleKindNone = ""
	// HandleKindPaymentLink is a plink_ handle with a short URL.
	HandleKindPaymentLink = "payment_link"
	// HandleKindInvoice is an inv_ handle. An issued invoice carries its own
	// short URL, so it is already payable and does not need a second link.
	HandleKindInvoice = "invoice"
)

// The lawful action set, and nothing else.
//
// There is no retry action of any kind, and adding one is not a matter of
// adding a constant. Unattended retry of a one-off payment is not lawful in
// India: a card debit without the cardholder present needs either an
// additional factor of authentication or a registered e-mandate, and neither
// exists for an order the customer walked away from. This engine has deleted
// the concept rather than gating it, so that no code path anywhere can reach
// it by passing a string.
//
// IsLawfulAction is the gate. An Intervention that is handed an action outside
// this set returns an Outcome with Accepted false rather than guessing.
const (
	// ActionNotifyEmail asks Razorpay to send the customer an email about an
	// existing handle. What is observable is that the notification API call
	// succeeded, not that a person read anything.
	ActionNotifyEmail = "notify_email"
	// ActionNotifySMS is the same over SMS.
	ActionNotifySMS = "notify_sms"
	// ActionCreatePaymentLink mints a new payment link for an item whose
	// PayHandle.Kind is HandleKindNone. The customer pays by choosing to.
	ActionCreatePaymentLink = "create_payment_link"
	// ActionResendLink resends the notification for a handle the item already
	// has. It creates nothing.
	ActionResendLink = "resend_link"
	// ActionLogPromise records that a customer said they will pay. It writes
	// an audit row and touches no Razorpay resource.
	ActionLogPromise = "log_promise"
	// ActionEscalate hands the item to a person. It is a refusal to act
	// automatically, not a softer action.
	ActionEscalate = "escalate"
	// ActionCancelWriteOff closes the item as not collectable. It is
	// terminal.
	ActionCancelWriteOff = "cancel_write_off"
	// ActionDoNothing is the explicit no-op, so that an audit row never has to
	// be read as "nothing was decided, presumably that was fine".
	ActionDoNothing = "do_nothing"
)

// lawfulActions is the closed set behind IsLawfulAction and LawfulActions.
var lawfulActions = []string{
	ActionNotifyEmail,
	ActionNotifySMS,
	ActionCreatePaymentLink,
	ActionResendLink,
	ActionLogPromise,
	ActionEscalate,
	ActionCancelWriteOff,
	ActionDoNothing,
}

// LawfulActions returns the closed action set, in declaration order. The
// returned slice is a copy, so a caller cannot widen the set by writing to it.
func LawfulActions() []string {
	out := make([]string, len(lawfulActions))
	copy(out, lawfulActions)
	return out
}

// IsLawfulAction reports whether action is in the closed set. Anything else,
// including any spelling of a retry, is unlawful here.
func IsLawfulAction(action string) bool {
	return slices.Contains(lawfulActions, action)
}

// Customer is the contact detail Razorpay carried on the source resource.
//
// Every field is optional and every field is copied, never derived. An order
// created without a customer has an empty Customer, and no detector may fill
// it in from a similar order, a receipt, or a note. See HasContactChannel.
type Customer struct {
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
	Contact string `json:"contact,omitempty"`
}

// HasContactChannel reports whether there is somewhere to send a notification.
//
// An item with no channel must never have one guessed. It can still be
// escalated, written off, or left alone, and those are the only lawful
// outcomes for it. A notify action on an item where this is false is a defect
// in the caller, and an Intervention returns Accepted false rather than
// inventing an address.
func (c Customer) HasContactChannel() bool {
	return c.Email != "" || c.Contact != ""
}

// Signal carries the optional evidence a detector saw, flat rather than in a
// per-source union, so that the policy gate can read one field without knowing
// which detector produced the item.
//
// A field a source does not have stays at its zero value. An empty
// FailureCode means no failure was observed, not that a failure had no code.
type Signal struct {
	// FailureCode is error.code on a failed payment, such as
	// BAD_REQUEST_ERROR. It is a coarse class and is not a decline reason.
	FailureCode string `json:"failure_code,omitempty"`
	// FailureReason is error.reason. It is the field a classifier reads
	// first.
	FailureReason string `json:"failure_reason,omitempty"`
	// FailureSource is error.source, such as gateway.
	FailureSource string `json:"failure_source,omitempty"`
	// FailureStep is error.step, such as payment_authorization.
	FailureStep string `json:"failure_step,omitempty"`
	// Method is the instrument the customer chose, such as card or upi.
	Method string `json:"method,omitempty"`
	// Attempts is the order's attempt count. Zero on an order nobody tried,
	// which is what separates an abandonment from a failure.
	Attempts int `json:"attempts"`
	// EmailStatus is the invoice email_status Razorpay reports, such as sent.
	// It says the send was accepted. It does not say a person read anything.
	EmailStatus string `json:"email_status,omitempty"`
	// SmsStatus is the same field for SMS.
	SmsStatus string `json:"sms_status,omitempty"`
}

// PayHandle is the thing a customer can already pay against, if one exists.
//
// Kind is HandleKindNone when there is nothing, and then URL and ID are empty
// too. A detector never mints a handle: it reports the one the source resource
// carries, and creating one is ActionCreatePaymentLink's job.
type PayHandle struct {
	Kind string `json:"kind,omitempty"`
	URL  string `json:"url,omitempty"`
	ID   string `json:"id,omitempty"`
}

// RiskItem is one debt, seen by one detector, in the units Razorpay reports.
// All amounts are in paise.
//
// Two rules govern identity, and they are not the same rule.
//
// ID is per detector sighting. It is derived from Source and SourceID, so the
// same failed payment seen twice is one ID, and the same debt seen by two
// detectors is two IDs.
//
// RootOrderID is the dedupe key. Razorpay mints an order_id when an invoice is
// issued, so one debt is reachable from the invoice detector and from the
// unpaid-order detector at the same time, under two different SourceIDs. The
// queue collapses on DedupeKey, not on ID, or the customer is contacted twice
// about one debt. RootOrderID is empty only when the source resource has no
// order behind it at all, and then DedupeKey falls back to the sighting.
type RiskItem struct {
	// ID is "ri_" plus the first 12 hex characters of the SHA-256 of
	// Source, a vertical bar, and SourceID. Build it with NewID.
	ID string `json:"id"`
	// Source is the detector that produced this sighting.
	Source Source `json:"source"`
	// SourceID is the Razorpay id of the resource the detector read: a
	// payment id, an order id, or an invoice id.
	SourceID string `json:"source_id"`
	// RootOrderID is the order the debt sits on, and the dedupe key.
	RootOrderID string `json:"root_order_id,omitempty"`
	// Customer is the contact detail the source resource carried, copied and
	// never derived.
	Customer Customer `json:"customer"`
	// AmountPaise is the full amount of the debt.
	AmountPaise int64 `json:"amount_paise"`
	// AmountPaidPaise is what has been collected against it.
	AmountPaidPaise int64 `json:"amount_paid_paise"`
	// AmountDuePaise is what is still outstanding. It is carried rather than
	// computed because Razorpay reports it, and a partial payment can make
	// the arithmetic disagree with the gateway.
	AmountDuePaise int64 `json:"amount_due_paise"`
	// Currency is the ISO code Razorpay reported, such as INR.
	Currency string `json:"currency"`
	// AtRiskSince is the Unix second the debt started being at risk: the
	// failed payment's created_at, the order's created_at, or the invoice's
	// issued_at.
	AtRiskSince int64 `json:"at_risk_since"`
	// Signal is the optional evidence behind the sighting.
	Signal Signal `json:"signal"`
	// PayHandle is what the customer can already pay against, if anything.
	PayHandle PayHandle `json:"pay_handle"`
}

// NewID returns the deterministic id for a detector sighting.
//
// It is "ri_" plus the first 12 hex characters of the SHA-256 of source, a
// vertical bar, and sourceID. The bar is inside the hash so that a source and
// an id cannot be shuffled across the boundary into the same digest.
//
// The same sighting always produces the same id, which is what lets a run that
// is interrupted and repeated write the same audit rows. The id is not the
// dedupe key: see RiskItem.DedupeKey.
func NewID(source Source, sourceID string) string {
	sum := sha256.Sum256([]byte(string(source) + "|" + sourceID))
	return "ri_" + hex.EncodeToString(sum[:])[:12]
}

// DedupeKey returns the key the risk queue collapses on.
//
// It is RootOrderID when there is one, because an issued invoice and the order
// it minted are one debt reached by two detectors. It falls back to the
// sighting's own identity when the source resource has no order behind it, so
// that an item with no root order is never merged with an unrelated one.
func (r RiskItem) DedupeKey() string {
	if r.RootOrderID != "" {
		return r.RootOrderID
	}
	return string(r.Source) + "|" + r.SourceID
}

// Detector finds debt of one kind. Every detector returns the same item type,
// which is the point of this package.
//
// Detect returns the items it could read. A detector that reads a page and
// then fails partway returns what it has and the error, and the caller decides
// whether a partial sweep is worth acting on.
type Detector interface {
	// Name is the stable identifier for this detector in the audit trail.
	Name() string
	// Detect returns the items visible now.
	Detect(ctx context.Context) ([]RiskItem, error)
}

// Outcome is one audit row: what an intervention did, and what can be observed
// about it afterwards.
//
// Accepted records that the API call the action makes succeeded. It does not
// record that a customer was told anything, and no field here should ever be
// read that way. Observable is the strongest thing that was actually seen,
// written as a field and a value, such as "email_status:sent" or
// "plink_status:created".
type Outcome struct {
	// Action is the action that was applied, from the lawful set.
	Action string `json:"action"`
	// Accepted reports that the call succeeded. An unlawful action, an item
	// with no contact channel, or a refusal all leave this false.
	Accepted bool `json:"accepted"`
	// Observable is what was seen, as field:value. Empty when nothing was
	// observable, which is the honest answer for ActionDoNothing.
	Observable string `json:"observable,omitempty"`
	// Handle is the link the action created or reused, when there was one.
	// The tag is omitzero rather than omitempty because omitempty does
	// nothing on a struct field, and an audit row full of empty handles is
	// noise a reader has to skip.
	Handle PayHandle `json:"handle,omitzero"`
	// At is when the call was made.
	At time.Time `json:"at"`
	// Err is the error text, when there was one. It is a string rather than
	// an error so that an Outcome round-trips through JSON into the audit
	// trail unchanged.
	Err string `json:"error,omitempty"`
}

// Intervention applies one lawful action to one item.
//
// Apply returns an Outcome for every call, including a refusal, so that the
// audit trail has a row for everything that was decided. An implementation
// that refuses returns an Outcome with Accepted false and a reason in Err, and
// returns a non-nil error only when the call itself could not be made.
//
// An implementation must refuse rather than guess when IsLawfulAction is false
// for the action, or when a notify action arrives for an item whose
// Customer.HasContactChannel is false.
type Intervention interface {
	Apply(ctx context.Context, item RiskItem, action string) (Outcome, error)
}
