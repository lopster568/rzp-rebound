package riskrun

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// PollAPI is the slice of the Razorpay client a snapshot reads through: one
// fetch per kind of thing a run can leave behind.
//
// *razorpay.Client satisfies it. A snapshot makes one read per entity and
// writes nothing, so nothing here can move money or send a message.
type PollAPI interface {
	FetchInvoice(ctx context.Context, invoiceID string) (razorpay.Invoice, error)
	FetchOrder(ctx context.Context, orderID string) (razorpay.Order, error)
	FetchPaymentLink(ctx context.Context, linkID string) (razorpay.PaymentLinkDetail, error)
}

var _ PollAPI = (*razorpay.Client)(nil)

// The entity kinds a snapshot entry can be for.
const (
	EntryInvoice     = "invoice"
	EntryOrder       = "order"
	EntryPaymentLink = "payment_link"
)

// SnapshotEntry is one entity as Razorpay reported it at one instant.
//
// Every amount is Razorpay's own. AmountDuePaise in particular is read off the
// response rather than computed from the other two, for the reason
// riskitem.RiskItem gives for carrying it: a partial payment makes the
// subtraction disagree with the gateway, and the gateway is the one that
// decides what is still owed.
//
// A payment link reports no amount_due at all, so the field is left at zero on
// one rather than filled in by arithmetic. The paid figure is what a delta
// between two snapshots reads.
type SnapshotEntry struct {
	Kind            string `json:"kind"`
	ID              string `json:"id"`
	OrderID         string `json:"order_id,omitempty"`
	Status          string `json:"status,omitempty"`
	AmountPaise     int64  `json:"amount_paise"`
	AmountPaidPaise int64  `json:"amount_paid_paise"`
	AmountDuePaise  int64  `json:"amount_due_paise"`
	Currency        string `json:"currency,omitempty"`
	// EmailStatus and SMSStatus are the invoice fields that report a send.
	// They are the only fields anywhere that separate a notification this
	// account asked Razorpay to send from one it did not, and a value of sent
	// is Razorpay reporting it sent something rather than a person having read
	// anything.
	EmailStatus string `json:"email_status,omitempty"`
	SMSStatus   string `json:"sms_status,omitempty"`
	// Error is the read that failed, when one did. The entry stays in the file:
	// an entity that could not be read is not an entity that is settled, and
	// dropping it would let a delta count it as paid.
	Error string `json:"error,omitempty"`
}

// Snapshot is every manifest entity as Razorpay reported it at one instant.
//
// Two snapshots are what a recovered-paise figure is made of, and neither one
// alone is: the paid column moves for reasons this program did not cause, and
// the honest statement is the difference between two reads with the
// intervention between them.
type Snapshot struct {
	TakenAt        time.Time       `json:"taken_at"`
	ManifestPath   string          `json:"manifest_path"`
	ManifestRunTag string          `json:"manifest_run_tag,omitempty"`
	Entries        []SnapshotEntry `json:"entries"`
	Totals         SnapshotTotals  `json:"totals"`
}

// SnapshotTotals sums the entries, and counts the ones that could not be read.
type SnapshotTotals struct {
	Entries         int   `json:"entries"`
	Errors          int   `json:"errors"`
	AmountPaise     int64 `json:"amount_paise"`
	AmountPaidPaise int64 `json:"amount_paid_paise"`
	AmountDuePaise  int64 `json:"amount_due_paise"`
}

// PollOptions configures a snapshot.
type PollOptions struct {
	// Manifest is the seed run to re-read.
	Manifest seed.Manifest
	// ManifestPath is recorded in the snapshot.
	ManifestPath string
	// PaymentLinkIDs are links a risk run created, which the manifest does not
	// know about because the seeder did not make them. Empty is the ordinary
	// case.
	PaymentLinkIDs []string
	// Clock stamps the snapshot. Nil means the wall clock.
	Now time.Time
}

// Poll re-reads every manifest item and returns the snapshot.
//
// An invoice contributes two reads, the invoice and the order it minted,
// because they are two different answers about one debt: the invoice carries
// the notification-status fields and the order is what a payment lands on. A
// read that fails leaves an entry carrying the error rather than no entry at
// all, and the run carries on, because a snapshot that stopped at the first
// unreadable entity would be a partial file that looks complete.
func Poll(ctx context.Context, api PollAPI, opts PollOptions) (Snapshot, error) {
	if api == nil {
		return Snapshot{}, fmt.Errorf("riskrun: a snapshot needs a Razorpay client")
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	snapshot := Snapshot{
		TakenAt:        now.UTC(),
		ManifestPath:   opts.ManifestPath,
		ManifestRunTag: opts.Manifest.RunTag,
	}

	seen := make(map[string]bool)
	add := func(entry SnapshotEntry) {
		key := entry.Kind + "|" + entry.ID
		if entry.ID == "" || seen[key] {
			return
		}
		seen[key] = true
		snapshot.Entries = append(snapshot.Entries, entry)
	}

	for _, item := range opts.Manifest.Items {
		switch item.Kind {
		case seed.EntityInvoice:
			add(fetchInvoiceEntry(ctx, api, item.ID))
			if item.OrderID != "" {
				add(fetchOrderEntry(ctx, api, item.OrderID))
			}
		case seed.EntityOrder:
			add(fetchOrderEntry(ctx, api, item.ID))
		}
	}
	for _, linkID := range slices.Sorted(slices.Values(opts.PaymentLinkIDs)) {
		add(fetchPaymentLinkEntry(ctx, api, linkID))
	}

	for _, entry := range snapshot.Entries {
		snapshot.Totals.Entries++
		if entry.Error != "" {
			snapshot.Totals.Errors++
			continue
		}
		snapshot.Totals.AmountPaise += entry.AmountPaise
		snapshot.Totals.AmountPaidPaise += entry.AmountPaidPaise
		snapshot.Totals.AmountDuePaise += entry.AmountDuePaise
	}
	return snapshot, nil
}

func fetchInvoiceEntry(ctx context.Context, api PollAPI, id string) SnapshotEntry {
	entry := SnapshotEntry{Kind: EntryInvoice, ID: id}
	invoice, err := api.FetchInvoice(ctx, id)
	if err != nil {
		// Errors out of internal/razorpay have been through Client.Redact
		// before they get here, which is the control that keeps a credential
		// out of this file.
		entry.Error = err.Error()
		return entry
	}
	entry.OrderID = invoice.OrderID
	entry.Status = invoice.Status
	entry.AmountPaise = invoice.AmountPaise
	entry.AmountPaidPaise = invoice.AmountPaid
	entry.AmountDuePaise = invoice.AmountDue
	entry.Currency = invoice.Currency
	entry.EmailStatus = invoice.EmailStatus
	entry.SMSStatus = invoice.SMSStatus
	return entry
}

func fetchOrderEntry(ctx context.Context, api PollAPI, id string) SnapshotEntry {
	entry := SnapshotEntry{Kind: EntryOrder, ID: id}
	order, err := api.FetchOrder(ctx, id)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.Status = order.Status
	entry.AmountPaise = order.AmountPaise
	entry.AmountPaidPaise = order.AmountPaid
	entry.AmountDuePaise = order.AmountDue
	entry.Currency = order.Currency
	return entry
}

func fetchPaymentLinkEntry(ctx context.Context, api PollAPI, id string) SnapshotEntry {
	entry := SnapshotEntry{Kind: EntryPaymentLink, ID: id}
	link, err := api.FetchPaymentLink(ctx, id)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.Status = link.Status
	entry.AmountPaise = link.AmountPaise
	entry.AmountPaidPaise = link.AmountPaid
	entry.Currency = link.Currency
	return entry
}

// Delta is the difference between two snapshots of the same book.
type Delta struct {
	FromTakenAt      time.Time `json:"from_taken_at"`
	ToTakenAt        time.Time `json:"to_taken_at"`
	RecoveredPaise   int64     `json:"recovered_paise"`
	AmountDueChange  int64     `json:"amount_due_change_paise"`
	EntriesCompared  int       `json:"entries_compared"`
	EntriesUnmatched int       `json:"entries_unmatched"`
	// StatusChanges names the entities whose status moved, oldest status
	// first, as "id: before -> after".
	StatusChanges []string `json:"status_changes,omitempty"`
}

// Diff reports what moved between two snapshots.
//
// RecoveredPaise is the rise in what Razorpay reports as collected, summed over
// the entities both snapshots could read. It is not a claim about what this
// program caused: a customer who paid for their own reasons moves the same
// number, and the control arm exists so that the question can be asked of two
// groups rather than of one.
//
// An entity present in one snapshot and not the other, or unreadable in either,
// is counted as unmatched and contributes nothing. A read that failed is not a
// zero.
func Diff(before, after Snapshot) Delta {
	index := make(map[string]SnapshotEntry, len(before.Entries))
	for _, entry := range before.Entries {
		index[entry.Kind+"|"+entry.ID] = entry
	}

	delta := Delta{FromTakenAt: before.TakenAt, ToTakenAt: after.TakenAt}
	for _, entry := range after.Entries {
		earlier, ok := index[entry.Kind+"|"+entry.ID]
		if !ok || entry.Error != "" || earlier.Error != "" {
			delta.EntriesUnmatched++
			continue
		}
		delta.EntriesCompared++
		delta.RecoveredPaise += entry.AmountPaidPaise - earlier.AmountPaidPaise
		delta.AmountDueChange += entry.AmountDuePaise - earlier.AmountDuePaise
		if earlier.Status != entry.Status {
			delta.StatusChanges = append(delta.StatusChanges,
				fmt.Sprintf("%s: %s -> %s", entry.ID, earlier.Status, entry.Status))
		}
	}
	return delta
}
