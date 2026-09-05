package intervene

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// Refusal reasons. Every string this package puts in riskitem.Outcome.Err for
// a refusal comes from this block, so what the engine is willing to say about
// declining to act is a short list a reviewer can read in full.
//
// The first two are the refusals riskitem's package doc requires of every
// Intervention, spelled the way the frozen contract's own reference stub
// spells them.
const (
	RefusalUnlawfulAction    = "action is not in the lawful set"
	RefusalNoContactChannel  = "item has no contact channel"
	RefusalNoHandle          = "item has nothing to notify about: no payment link and no invoice"
	RefusalHandleExists      = "item already has a handle, so a new payment link would be a second thing to pay"
	RefusalNoAmount          = "item has no positive amount to collect"
	RefusalNotAnInvoice      = "cancel_write_off is only lawful for an invoice-handled item"
	RefusalGatewayNotAccept  = "the gateway answered without accepting the notification API call"
	RefusalNoActionAvailable = "no implementation for this lawful action"
)

// Observable values, in the field:value form riskitem.Outcome documents.
//
// None of them describes a person. The strongest thing this system sees for a
// notification is Razorpay's HTTP response, plus, for an invoice, the
// email_status or sms_status field on a later read of that invoice. A status
// of sent is Razorpay reporting it sent something. It is not a person having
// read anything, and no value here should be read that way.
const (
	// ObservableNotifyAccepted is what a notification is worth when there is
	// no field to read back: the notification API call succeeded.
	ObservableNotifyAccepted = "notify_api:accepted"
	// ObservableNotifyRefused is a 2xx that carried success false.
	ObservableNotifyRefused = "notify_api:refused"
	// ObservableNotifyFailed is a call that did not complete.
	ObservableNotifyFailed = "notify_api:failed"
	// ObservableEscalated is an escalation the sink accepted.
	ObservableEscalated = "escalated:queued"
	// ObservableCreateAccepted is a payment link the create call returned with
	// no status on it: the call succeeded and the response said nothing about
	// the link's state.
	ObservableCreateAccepted = "plink_api:accepted"
)

// Observable field names, used with a value read off the gateway response.
const (
	observableFieldEmailStatus   = "email_status"
	observableFieldSMSStatus     = "sms_status"
	observableFieldLinkStatus    = "plink_status"
	observableFieldInvoiceStatus = "invoice_status"
	observableFieldPromiseHold   = "promise_hold_until"
)

// DefaultLinkDescription is what a created payment link carries when
// Options.LinkDescription is not set. It is a configured choice: the
// description is customer-facing, so it names the debt in plain words rather
// than carrying an internal id. The internal id goes on reference_id.
const DefaultLinkDescription = "Outstanding payment"

// Errors New returns.
var (
	ErrNoGateway  = errors.New("intervene: needs a Gateway")
	ErrNoRecorder = errors.New("intervene: needs an audit recorder")
)

// Gateway is the slice of the Razorpay client this package calls. It is
// narrower than razorpay.Port because that interface holds calls no
// intervention makes and is missing three that every invoice action needs.
//
// The compile-time assertion below is the point of the interface: the live
// client satisfies it, so the shapes here cannot drift from the ones that were
// probed on 2026-09-05.
type Gateway interface {
	NotifyInvoice(ctx context.Context, invoiceID, medium string) (razorpay.NotifyReceipt, error)
	FetchInvoice(ctx context.Context, invoiceID string) (razorpay.Invoice, error)
	CancelInvoice(ctx context.Context, invoiceID string) (razorpay.Invoice, error)
	CreatePaymentLink(ctx context.Context, req razorpay.CreatePaymentLinkRequest) (razorpay.PaymentLink, error)
	ResendPaymentLinkNotification(ctx context.Context, linkID, medium string) (razorpay.NotifyReceipt, error)
}

var _ Gateway = (*razorpay.Client)(nil)

// Options configures an Engine.
type Options struct {
	// Gateway is what the side-effecting actions call. Required.
	Gateway Gateway
	// Recorder is the audit trail. Required: every Apply writes one row, and
	// an engine that could be built without one could act without a record.
	Recorder *audit.Recorder
	// Promises is where log_promise writes. Required rather than defaulted,
	// so a promise cannot be logged into a ledger the caller cannot read.
	Promises PromiseLedger
	// Escalations is where escalate writes. Required for the same reason: an
	// escalation nobody can read is the failure mode the sink exists to fix.
	Escalations EscalationSink
	// Clock stamps every Outcome and every record. Nil means the wall clock.
	Clock clock.Clock
	// PromiseHold is how long a logged promise holds an item. Zero means
	// DefaultPromiseHold.
	PromiseHold time.Duration
	// LinkDescription is the customer-facing description on a created payment
	// link. Empty means DefaultLinkDescription.
	LinkDescription string
}

// Engine applies one lawful action to one risk item.
//
// It is riskitem.Intervention. Every Apply returns an Outcome, including every
// refusal, and every Apply writes exactly one audit row:
//
//	accepted escalate                  audit.KindEscalationRaised
//	accepted log_promise               audit.KindPromiseLogged
//	anything else that was attempted   audit.KindInterventionApplied
//	refused, do_nothing, or a replay   audit.KindActionSkipped
//
// The split is on whether anything was called, not on whether it worked. An
// action that ran and failed is an attempt and shares a kind with one that
// succeeded; a refusal and the explicit no-op called nothing. That is phase
// 1's decision of 2026-08-31, made when a failed action was being filed as a
// skipped one and the scoring pass, which counts attempts against refusals,
// was undercounting attempts.
//
// The idempotency guard is the other thing only the ledger shows: a replay
// returns the first call's Outcome unchanged and writes a skipped row carrying
// idempotent_replay.
type Engine struct {
	gateway     Gateway
	recorder    *audit.Recorder
	promises    PromiseLedger
	escalations EscalationSink
	clock       clock.Clock
	promiseHold time.Duration
	linkDesc    string

	guard *guard
}

var _ riskitem.Intervention = (*Engine)(nil)

// New returns an Engine.
func New(opts Options) (*Engine, error) {
	if opts.Gateway == nil {
		return nil, ErrNoGateway
	}
	if opts.Recorder == nil {
		return nil, ErrNoRecorder
	}
	if opts.Promises == nil {
		return nil, ErrNoPromiseLedger
	}
	if opts.Escalations == nil {
		return nil, ErrNoEscalationSink
	}

	c := opts.Clock
	if c == nil {
		c = clock.Real()
	}
	hold := opts.PromiseHold
	if hold <= 0 {
		hold = DefaultPromiseHold
	}
	desc := opts.LinkDescription
	if desc == "" {
		desc = DefaultLinkDescription
	}

	return &Engine{
		gateway:     opts.Gateway,
		recorder:    opts.Recorder,
		promises:    opts.Promises,
		escalations: opts.Escalations,
		clock:       c,
		promiseHold: hold,
		linkDesc:    desc,
		guard:       newGuard(),
	}, nil
}

// Apply applies action to item.
//
// It returns an Outcome for every call. Accepted false with a reason in Err is
// a refusal, and it is not an error: the engine decided not to act and the
// audit row says so. A non-nil error means the call could not be completed,
// with one exception, an audit write that failed: the side effect may already
// have happened, and a side effect with no row is what the ledger exists to
// prevent, so it is reported rather than swallowed.
//
// A verifying read that failed after an accepted notification is not an error.
// The action succeeded, so Accepted stays true, the observable drops to the
// weaker ObservableNotifyAccepted, and the read's error text goes in Err.
//
// Two calls carrying one idempotency key are one decision. The second does not
// call anything and returns the first call's Outcome.
func (e *Engine) Apply(ctx context.Context, item riskitem.RiskItem, action string) (riskitem.Outcome, error) {
	out := riskitem.Outcome{Action: action, At: e.clock.Now()}

	// The gate riskitem's package doc requires. It runs before the idempotency
	// key is computed, because an unlawful action is not a decision worth
	// remembering.
	if !riskitem.IsLawfulAction(action) {
		out.Err = RefusalUnlawfulAction
		return out, e.record(ctx, item, out, "", false, false)
	}

	key := e.idempotencyKey(ctx, item, action)

	// The slot is held for the whole action, not just for the lookup. Taking
	// the answer and then acting on it is a check-then-act: two goroutines
	// sweeping the same key both miss, and both send. Holding it means the
	// second one waits and then finds the first one's result, which is the
	// point of the key. Only calls sharing a key contend.
	slot := e.guard.slot(key)
	slot.mu.Lock()
	defer slot.mu.Unlock()

	if slot.done {
		// The effect exists and it is the first call's effect, so the first
		// call's Outcome is what is true about it: the same handle, the same
		// observable, and the instant it actually happened. Nothing is called
		// again. The replay is visible in the ledger and not in the Outcome.
		return slot.out, e.record(ctx, item, slot.out, key, false, true)
	}

	out, attempted, callErr := e.dispatch(ctx, item, action, out)
	if callErr != nil && out.Err == "" {
		// Errors out of internal/razorpay have already been through
		// Client.Redact, which is the control. audit.RedactValue is the
		// backstop underneath it.
		out.Err = callErr.Error()
	}
	// The slot is sealed before the row is written, and that ordering is a
	// choice between two bad outcomes. Sealing after a successful record would
	// mean a failed audit write leaves the key open, so a caller that retries
	// sends a second notification or mints a second link. Sealing first means
	// a failed audit write leaves the effect recorded only by the replay row
	// the retry writes, which names the action as skipped. A duplicated side
	// effect is worse than a row that undersells one, and the audit failure is
	// returned to the caller either way.
	if out.Accepted && guarded(action) {
		slot.out = out
		slot.done = true
	}

	return out, errors.Join(callErr, e.record(ctx, item, out, key, attempted, false))
}

// dispatch routes one lawful action. It never sees an unlawful one.
//
// The middle return says whether anything was called: a gateway request, a
// promise ledger write, or an escalation sink write. It is false for every
// refusal that happens before the call and for the explicit no-op, and true
// for a call that was made and failed. record turns it into the audit kind.
func (e *Engine) dispatch(ctx context.Context, item riskitem.RiskItem, action string, out riskitem.Outcome) (riskitem.Outcome, bool, error) {
	switch action {
	case riskitem.ActionNotifyEmail:
		return e.notify(ctx, item, out, razorpay.MediumEmail)
	case riskitem.ActionNotifySMS:
		return e.notify(ctx, item, out, razorpay.MediumSMS)
	case riskitem.ActionResendLink:
		return e.notify(ctx, item, out, preferredMedium(item))
	case riskitem.ActionCreatePaymentLink:
		return e.createPaymentLink(ctx, item, out)
	case riskitem.ActionLogPromise:
		return e.logPromise(ctx, item, out)
	case riskitem.ActionEscalate:
		return e.escalate(ctx, item, out)
	case riskitem.ActionCancelWriteOff:
		return e.cancelWriteOff(ctx, item, out)
	case riskitem.ActionDoNothing:
		// The explicit no-op. Accepted, nothing called, and Observable stays
		// empty, which riskitem.Outcome names as the honest answer for it.
		out.Accepted = true
		return out, false, nil
	default:
		// Only reachable if a constant is added to riskitem's lawful set
		// without a case here. Refusing is the safe answer: the alternative is
		// an action the engine does not implement being reported as done.
		out.Err = RefusalNoActionAvailable
		return out, false, nil
	}
}

// notify sends an existing handle again, over medium.
//
// The contact-channel gate is riskitem's, and it is the only thing here that
// can refuse on contact. Razorpay sends to the contact details on the invoice
// or on the payment link, not to riskitem.Customer, so refusing an email
// notify because this item carries a phone number and no address would be this
// package guessing about a resource it has not read. preferredMedium reads the
// same fields, but only to choose between two media a caller did not name,
// which is a choice rather than a refusal.
func (e *Engine) notify(ctx context.Context, item riskitem.RiskItem, out riskitem.Outcome, medium string) (riskitem.Outcome, bool, error) {
	if !item.Customer.HasContactChannel() {
		out.Err = RefusalNoContactChannel
		return out, false, nil
	}
	if item.PayHandle.ID == "" {
		out.Err = RefusalNoHandle
		return out, false, nil
	}

	switch item.PayHandle.Kind {
	case riskitem.HandleKindInvoice:
		return e.notifyInvoice(ctx, item, out, medium)
	case riskitem.HandleKindPaymentLink:
		return e.resendPaymentLink(ctx, item, out, medium)
	default:
		out.Err = RefusalNoHandle
		return out, false, nil
	}
}

// notifyInvoice asks Razorpay to send an issued invoice, then reads the
// invoice back to see what it says about the send.
//
// The read-back is the whole difference between this and the payment link
// path. NotifyInvoice answering {"success":true} means the API accepted the
// call. email_status on a later fetch is the only field Razorpay carries that
// separates a notify that was made from one that was not, and it moved from
// null to sent on the 2026-09-05 probe. Neither one says a person read
// anything.
func (e *Engine) notifyInvoice(ctx context.Context, item riskitem.RiskItem, out riskitem.Outcome, medium string) (riskitem.Outcome, bool, error) {
	id := item.PayHandle.ID

	receipt, err := e.gateway.NotifyInvoice(ctx, id, medium)
	if err != nil {
		if gatewayDeclined(err) {
			out.Observable = ObservableNotifyRefused
			out.Err = err.Error()
			return out, true, nil
		}
		out.Observable = ObservableNotifyFailed
		return out, true, fmt.Errorf("intervene: notify invoice %s over %s: %w", id, medium, err)
	}
	if !receipt.Accepted {
		out.Observable = ObservableNotifyRefused
		out.Err = RefusalGatewayNotAccept
		return out, true, nil
	}

	out.Accepted = true
	out.Handle = item.PayHandle

	invoice, err := e.gateway.FetchInvoice(ctx, id)
	if err != nil {
		// The notification API call succeeded and the verifying read did not.
		// Downgrading Accepted here would report the send as not having
		// happened, which is a different and wrong claim, so the weaker
		// observable and the read's error carry the difference.
		out.Observable = ObservableNotifyAccepted
		out.Err = fmt.Sprintf("the notification API call succeeded and the verifying read failed: %v", err)
		return out, true, nil
	}

	field, status := notifyStatus(invoice, medium)
	if status == "" {
		// Both status fields are null until Razorpay has sent. A read that
		// finds one still empty is not evidence of a send, so it does not get
		// to claim one.
		out.Observable = ObservableNotifyAccepted
		return out, true, nil
	}
	out.Observable = field + ":" + status
	return out, true, nil
}

// resendPaymentLink resends a payment link.
//
// There is no field to read back. A payment link carries no equivalent of the
// invoice's email_status: the nearest thing is reminders.status, which read
// failed on 2026-09-05 for a link whose contact details test mode would not
// send to, so it reports the reminder machinery rather than this call. The
// strongest true observable is that the notification API call was accepted.
func (e *Engine) resendPaymentLink(ctx context.Context, item riskitem.RiskItem, out riskitem.Outcome, medium string) (riskitem.Outcome, bool, error) {
	id := item.PayHandle.ID

	receipt, err := e.gateway.ResendPaymentLinkNotification(ctx, id, medium)
	if err != nil {
		if gatewayDeclined(err) {
			out.Observable = ObservableNotifyRefused
			out.Err = err.Error()
			return out, true, nil
		}
		out.Observable = ObservableNotifyFailed
		return out, true, fmt.Errorf("intervene: resend payment link %s over %s: %w", id, medium, err)
	}
	if !receipt.Accepted {
		out.Observable = ObservableNotifyRefused
		out.Err = RefusalGatewayNotAccept
		return out, true, nil
	}

	out.Accepted = true
	out.Handle = item.PayHandle
	out.Observable = ObservableNotifyAccepted
	return out, true, nil
}

// createPaymentLink mints a link for an item with nothing to pay against.
//
// The notify flags are left false on purpose. CreatePaymentLinkRequest carries
// no customer object, so asking Razorpay to notify on creation would be asking
// it to send to a contact the request does not carry. Minting and sending are
// two actions in the lawful set, and resend_link is the one that sends.
func (e *Engine) createPaymentLink(ctx context.Context, item riskitem.RiskItem, out riskitem.Outcome) (riskitem.Outcome, bool, error) {
	if item.PayHandle.Kind != riskitem.HandleKindNone {
		out.Handle = item.PayHandle
		out.Err = RefusalHandleExists
		return out, false, nil
	}

	// AmountDuePaise is carried by Razorpay rather than computed, so it is the
	// figure to collect. A partially paid debt reports less due than its total,
	// and billing the total again would charge for what was already paid.
	//
	// The fallback is only taken when nothing has been collected. Razorpay
	// reports amount_due as null on a resource that is not yet issued, which
	// decodes to zero, so a zero due against a zero paid is a field the
	// detector could not fill. A zero due against a non-zero paid is a debt
	// that is settled, and minting a link for its full amount would bill a
	// customer a second time.
	amount := item.AmountDuePaise
	if amount <= 0 && item.AmountPaidPaise == 0 {
		amount = item.AmountPaise
	}
	if amount <= 0 {
		out.Err = RefusalNoAmount
		return out, false, nil
	}

	link, err := e.gateway.CreatePaymentLink(ctx, razorpay.CreatePaymentLinkRequest{
		AmountPaise: amount,
		Currency:    item.Currency,
		Description: e.linkDesc,
		// The sighting id, which is deterministic for a given source and
		// source id, so a re-run over the same item asks for the same
		// reference. Whether Razorpay refuses a duplicate reference_id has not
		// been probed here, so nothing downstream treats it as a gateway-side
		// idempotency key; the guard in this package is what stops a second
		// call.
		ReferenceID: item.ID,
	})
	if err != nil {
		if gatewayDeclined(err) {
			out.Err = err.Error()
			return out, true, nil
		}
		return out, true, fmt.Errorf("intervene: create a payment link for %s: %w", item.ID, err)
	}

	out.Accepted = true
	out.Handle = riskitem.PayHandle{
		Kind: riskitem.HandleKindPaymentLink,
		URL:  link.ShortURL,
		ID:   link.ID,
	}
	if link.Status == "" {
		// The status that came back and nothing else. Defaulting an absent
		// field to created would put an observation in the audit trail that
		// the response did not carry, which is the one thing Observable must
		// not do.
		out.Observable = ObservableCreateAccepted
		return out, true, nil
	}
	out.Observable = observableFieldLinkStatus + ":" + link.Status
	return out, true, nil
}

// cancelWriteOff closes an invoice-backed debt as not collectable.
//
// It is only lawful for an invoice-handled item. There is no write-off call
// for an order or for a payment link, and cancelling a payment link is not the
// same act, so anything else is refused rather than approximated.
func (e *Engine) cancelWriteOff(ctx context.Context, item riskitem.RiskItem, out riskitem.Outcome) (riskitem.Outcome, bool, error) {
	if item.PayHandle.Kind != riskitem.HandleKindInvoice || item.PayHandle.ID == "" {
		out.Err = RefusalNotAnInvoice
		return out, false, nil
	}

	invoice, err := e.gateway.CancelInvoice(ctx, item.PayHandle.ID)
	if err != nil {
		// A paid invoice cannot be cancelled and Razorpay answers that with a
		// 400. That is the gateway declining a call it read, not a call that
		// failed to happen.
		if gatewayDeclined(err) {
			out.Err = err.Error()
			return out, true, nil
		}
		return out, true, fmt.Errorf("intervene: cancel invoice %s: %w", item.PayHandle.ID, err)
	}

	out.Accepted = true
	out.Handle = item.PayHandle
	// The status that came back, not the status that was asked for. A cancel
	// that answered with something other than cancelled is worth seeing.
	out.Observable = observableFieldInvoiceStatus + ":" + invoice.Status
	return out, true, nil
}

// logPromise records that a customer said they will pay. It calls no gateway.
func (e *Engine) logPromise(ctx context.Context, item riskitem.RiskItem, out riskitem.Outcome) (riskitem.Outcome, bool, error) {
	rec := PromiseRecord{
		RiskItemID:     item.ID,
		PromisedAtUnix: out.At.Unix(),
		HoldUntilUnix:  out.At.Add(e.promiseHold).Unix(),
		Note:           reasonFor(ctx, item, riskitem.ActionLogPromise),
	}
	if err := e.promises.Log(ctx, rec); err != nil {
		return out, true, fmt.Errorf("intervene: log a promise for %s: %w", item.ID, err)
	}

	out.Accepted = true
	// Handle is left empty. riskitem.Outcome documents it as the link the
	// action created or reused, and this action did neither: it wrote a row
	// and touched no Razorpay resource.
	out.Observable = observableFieldPromiseHold + ":" + strconv.FormatInt(rec.HoldUntilUnix, 10)
	return out, true, nil
}

// escalate hands the item to a person. It calls no gateway.
func (e *Engine) escalate(ctx context.Context, item riskitem.RiskItem, out riskitem.Outcome) (riskitem.Outcome, bool, error) {
	esc := Escalation{
		RiskItemID:     item.ID,
		DedupeKey:      item.DedupeKey(),
		RootOrderID:    item.RootOrderID,
		Source:         string(item.Source),
		AmountDuePaise: item.AmountDuePaise,
		Currency:       item.Currency,
		Reason:         reasonFor(ctx, item, riskitem.ActionEscalate),
		RaisedAt:       out.At,
	}
	if err := e.escalations.Escalate(ctx, esc); err != nil {
		return out, true, fmt.Errorf("intervene: escalate %s: %w", item.ID, err)
	}

	out.Accepted = true
	// Handle is left empty, for the reason logPromise leaves it empty.
	out.Observable = ObservableEscalated
	return out, true, nil
}

// record writes the one audit row every Apply owes.
func (e *Engine) record(ctx context.Context, item riskitem.RiskItem, out riskitem.Outcome, key string, attempted, replay bool) error {
	detail := map[string]string{
		"risk_item_id": item.ID,
		"source":       string(item.Source),
		"accepted":     strconv.FormatBool(out.Accepted),
	}
	if out.Observable != "" {
		detail["observable"] = out.Observable
	}
	if out.Handle.Kind != "" {
		detail["handle_kind"] = out.Handle.Kind
	}
	if out.Handle.ID != "" {
		detail["handle_id"] = out.Handle.ID
	}
	if out.Handle.URL != "" {
		detail["handle_url"] = out.Handle.URL
	}
	if out.Err != "" {
		detail["error"] = out.Err
	}
	if key != "" {
		detail["idempotency_key"] = key
	}
	if replay {
		detail["idempotent_replay"] = "true"
	}

	if _, err := e.recorder.Record(ctx, audit.Event{
		OrderID:        joinKey(item),
		Kind:           eventKind(out, attempted, replay),
		ProposedAction: out.Action,
		Detail:         detail,
	}); err != nil {
		return fmt.Errorf("intervene: record %s on %s: %w", out.Action, item.ID, err)
	}
	return nil
}

// idempotencyKey is the key the guard collapses on.
//
// The action is appended whatever the caller supplied, so a key is always
// scoped to one action. A context is routinely reused across an item's whole
// decision, and without this a caller that escalated after notifying on one
// context would get the notify's Outcome back: the escalation would never
// happen and the ledger row would name the wrong action.
//
// Falling back to the sighting id means an engine driven with no key at all
// still cannot fire one action twice on one item, which is the safe direction.
// It also means a promise cannot be renewed on the fallback: a customer who
// promises, breaches, and promises again has the second promise collapsed into
// the first and the hold does not extend. A caller that wants a second
// notification a week later, or a renewed hold, supplies a key that says so.
func (e *Engine) idempotencyKey(ctx context.Context, item riskitem.RiskItem, action string) string {
	if key := IdempotencyKey(ctx); key != "" {
		return key + "|" + action
	}
	return item.ID + "|" + action
}

// guard holds one slot per idempotency key.
//
// It is in memory and per Engine. A restarted process has an empty guard and
// will fire an action it already fired, so a caller that needs idempotency
// across runs needs a durable store behind this. Nothing in this repository
// has one yet, and saying so is better than a guard that looks durable.
//
// Slots are never evicted. A sweep holds one entry per item and action, which
// is bounded by the batch; a process running for a long time over many batches
// would want an eviction policy, and there is none here.
//
// A slot's mutex is held across the gateway call, and sync.Mutex does not
// honour a context, so a caller with a deadline queued behind a slow call on
// the same key can sit past it. Only calls sharing a key contend, so this is
// one item's action waiting on itself, which is the behaviour the key asks
// for.
type guard struct {
	mu    sync.Mutex
	slots map[string]*slot
}

// slot is one key's mutex and the outcome held against it. The mutex is what
// serialises two Apply calls sharing a key, so the guard is a lock and not a
// lookup.
type slot struct {
	mu   sync.Mutex
	out  riskitem.Outcome
	done bool
}

func newGuard() *guard { return &guard{slots: make(map[string]*slot)} }

// slot returns the slot for key, creating it on first use. The guard's own
// lock is released before the caller takes the slot's, so one key's action
// never blocks another key's lookup.
func (g *guard) slot(key string) *slot {
	g.mu.Lock()
	defer g.mu.Unlock()

	s, ok := g.slots[key]
	if !ok {
		s = &slot{}
		g.slots[key] = s
	}
	return s
}

// guarded reports whether an accepted action leaves something behind that a
// second call would duplicate. Everything lawful does except do_nothing: the
// two ledger-writing actions leave a row, and the four gateway actions leave a
// notification, a link, or a cancelled invoice.
func guarded(action string) bool { return action != riskitem.ActionDoNothing }

// eventKind maps an Outcome to the audit vocabulary.
//
// It keys off attempted rather than off Accepted. An action that called
// something and failed is an attempt, and filing it as a skipped row would put
// it in the same bucket as a refusal, which the scoring pass counts against
// attempts. That is phase 1's decision of 2026-08-31, made after exactly this
// went wrong in internal/recovery, and it is the reason a failed notify below
// is an intervention rather than a skip.
func eventKind(out riskitem.Outcome, attempted, replay bool) string {
	switch {
	case replay:
		return audit.KindActionSkipped
	case !attempted:
		// A refusal or the explicit no-op. Nothing was called.
		return audit.KindActionSkipped
	case out.Accepted && out.Action == riskitem.ActionEscalate:
		return audit.KindEscalationRaised
	case out.Accepted && out.Action == riskitem.ActionLogPromise:
		return audit.KindPromiseLogged
	default:
		return audit.KindInterventionApplied
	}
}

// joinKey is what the audit row is joined on.
//
// It is riskitem's own dedupe key, which is already the root order when there
// is one and the sighting's identity when there is not. Writing that choice
// out a second time here would be a copy that can drift from the queue's, and
// then one debt would collapse in the queue under one key and land in the
// ledger under another.
//
// An item carrying neither a root order nor a source produces the bare
// separator, which the recorder accepts because it is not empty. That is a
// detector that returned an item with no identity, and it is not something
// this package can repair.
func joinKey(item riskitem.RiskItem) string { return item.DedupeKey() }

// gatewayDeclined reports whether err is the gateway answering the request
// rather than the request failing to get an answer.
//
// riskitem.Intervention says an implementation returns a non-nil error only
// when the call itself could not be made. A 4xx is a call that was made: the
// gateway read the request and refused it, and internal/razorpay does not
// retry one precisely because the same request gets the same refusal. That is
// a refusal with Accepted false, the same shape a 2xx carrying success false
// already gets, and the two must not diverge.
//
// A 5xx is not an answer about the request. Nothing here knows whether the
// side effect happened before the server failed, so it stays an error. So does
// an exhausted retry budget, which is a 429 that never got through, and so
// does anything with no HTTP response behind it at all.
func gatewayDeclined(err error) bool {
	if errors.Is(err, razorpay.ErrRetryBudgetExhausted) {
		return false
	}
	var apiErr *razorpay.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode >= 400 && apiErr.StatusCode < 500
}

// notifyStatus returns the invoice field that reports the send for medium, and
// the value on it. An empty value means Razorpay has not reported a send.
func notifyStatus(invoice razorpay.Invoice, medium string) (string, string) {
	if medium == razorpay.MediumSMS {
		return observableFieldSMSStatus, invoice.SMSStatus
	}
	return observableFieldEmailStatus, invoice.EmailStatus
}

// preferredMedium picks the medium for resend_link, which names none.
//
// Email when there is an address, SMS otherwise. The caller that wants the
// other one asks for notify_email or notify_sms by name.
func preferredMedium(item riskitem.RiskItem) string {
	if item.Customer.Email != "" {
		return razorpay.MediumEmail
	}
	return razorpay.MediumSMS
}

// reasonFor is the reason on the context, or a generated line naming the item
// and the action.
func reasonFor(ctx context.Context, item riskitem.RiskItem, action string) string {
	if reason := Reason(ctx); reason != "" {
		return reason
	}
	return fmt.Sprintf("%s for %s item %s", action, item.Source, item.ID)
}
