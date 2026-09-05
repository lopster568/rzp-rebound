package riskrun

import (
	"cmp"
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
	// DuplicateOf names the entry that already carries this debt's amounts.
	//
	// An issued invoice and the order Razorpay minted for it are one debt under
	// two ids, and both report the same three amounts. Summing both is how a
	// book worth INR 28338 read as INR 56676 gross on 2026-09-05, and it would
	// have reported a single paid invoice at twice its value in the delta.
	//
	// The amounts stay on the entry, because every amount in a snapshot is
	// Razorpay's own and blanking one would make the file disagree with the
	// gateway. What changes is that the totals and the money half of Diff skip
	// an entry carrying this field, so the debt is counted exactly once, on the
	// invoice. The order entry is still read, still shown, and still compared
	// for a status change, because created to paid on the order is the
	// transition a demo points at.
	DuplicateOf string `json:"duplicate_of,omitempty"`
	// DuplicateAskOf names the entry that already carries this entry's ask,
	// where the two do not mirror each other on payment.
	//
	// It is the weaker of the two markers and the difference is the whole point.
	// An invoice and its minted order mirror: a payment on either shows on both,
	// so DuplicateOf excludes all three of the order's amounts. A payment link a
	// run minted for an order does not mirror it. The link's reference_id is the
	// risk item, paying the link does not mark the order paid, and paying the
	// order does not mark the link paid, so the two are one ask reachable by two
	// routes and either route can collect.
	//
	// So an entry carrying this field is left out of the gross and the due,
	// where it would state the same debt twice, and its amount_paid is counted,
	// because it is the only place a payment made through that route is visible.
	// Summing the ask on both is how a book worth INR 49945 read as INR 51732
	// gross on 2026-09-05; dropping the paid figure with it would have made a
	// payment on the link read as no recovery at all.
	//
	// As with DuplicateOf, every amount Razorpay reported stays on the entry and
	// the entry stays in the file. Nothing here edits what the gateway said.
	DuplicateAskOf string `json:"duplicate_ask_of,omitempty"`
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
	// Skipped names the manifest items this snapshot could not ask Razorpay
	// about, one line each. They are listed rather than dropped: a seed run
	// that stopped partway leaves items with no gateway id on them, and a
	// snapshot that silently read fewer entities than the manifest holds is a
	// file that looks complete.
	Skipped []SkippedItem  `json:"skipped,omitempty"`
	Totals  SnapshotTotals `json:"totals"`
}

// SkippedItem is one manifest item no read was made for, and why.
type SkippedItem struct {
	Kind string `json:"kind"`
	// CustomerID is the one id an incomplete invoice item does carry, when the
	// seeder created the customer and stopped before the invoice. It is here so
	// the operator can find what exists in Razorpay.
	CustomerID string `json:"customer_id,omitempty"`
	Reason     string `json:"reason"`
}

// Reasons a manifest item is skipped.
const (
	// SkipNoID is an item the seed run never got an id for, which is what an
	// item marked seed.Item.Incomplete looks like.
	SkipNoID = "the manifest item carries no gateway id, so the seed run never created it"
	// SkipUnknownKind is an item whose kind this snapshot has no fetch for.
	SkipUnknownKind = "the manifest item has a kind this snapshot cannot read"
)

// SnapshotTotals sums the entries, and counts the ones that could not be read
// and the ones whose amounts belong to a debt another entry already carries.
//
// The three amount fields are summed over the entries that are neither, so the
// gross is what the book is worth rather than what its ids add up to.
type SnapshotTotals struct {
	Entries int `json:"entries"`
	Errors  int `json:"errors"`
	// Duplicates is how many entries carry DuplicateOf and were therefore left
	// out of all three sums below.
	Duplicates int `json:"duplicates"`
	// DuplicateAsks is how many entries carry DuplicateAskOf, so their gross and
	// their due were left out and their paid figure was counted. See
	// SnapshotEntry.DuplicateAskOf for why those are not the same exclusion.
	DuplicateAsks int `json:"duplicate_asks"`
	// Skipped is how many manifest items produced no read at all. It is the
	// length of Snapshot.Skipped, carried here so a reader comparing Entries
	// against the manifest's item count has the difference in the same block.
	Skipped         int   `json:"skipped"`
	AmountPaise     int64 `json:"amount_paise"`
	AmountPaidPaise int64 `json:"amount_paid_paise"`
	AmountDuePaise  int64 `json:"amount_due_paise"`
}

// MintedLink is one payment link a risk run created, and the debt it was
// minted against.
//
// DebtID is what makes the ask countable exactly once. The link's own
// reference_id is the risk item id, which is a hash of a source and a source id
// and names no Razorpay entity, so the id of the entity the link was raised for
// has to be carried here rather than read back off the link.
type MintedLink struct {
	// ID is the plink_ id.
	ID string
	// DebtID is the Razorpay id of the entity whose ask this link restates:
	// the order or the invoice the risk item was built from. Empty when the
	// run did not record one, and then the link's amounts are counted on their
	// own, because nothing says they are a second statement of anything.
	DebtID string
}

// PollOptions configures a snapshot.
type PollOptions struct {
	// Manifest is the seed run to re-read.
	Manifest seed.Manifest
	// ManifestPath is recorded in the snapshot.
	ManifestPath string
	// PaymentLinks are links a risk run created, which the manifest does not
	// know about because the seeder did not make them. Empty is the ordinary
	// case.
	PaymentLinks []MintedLink
	// Clock stamps the snapshot. Nil means the wall clock.
	Now time.Time
}

// Poll re-reads every manifest item and returns the snapshot.
//
// An invoice contributes two reads, the invoice and the order it minted,
// because they are two different answers about one debt: the invoice carries
// the notification-status fields and the order is what a payment lands on. The
// order entry is marked DuplicateOf the invoice for exactly that reason, so the
// debt reaches the totals once. A read that fails leaves an entry carrying the
// error rather than no entry at all, and the run carries on, because a snapshot
// that stopped at the first unreadable entity would be a partial file that
// looks complete.
//
// A payment link a run minted is marked DuplicateAskOf the entry that already
// carries the ask it restates, which is a weaker statement and a different
// exclusion: the link does not mirror that entity on payment, so its ask is
// dropped from the gross and its paid figure is kept. See the field's own doc
// comment.
//
// A manifest item with no gateway id on it produces no read and a line in
// Skipped instead. That is what a seed run which stopped partway leaves behind,
// and dropping it silently is how a snapshot comes to hold fewer entities than
// the manifest it names without saying so.
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
	// askOf maps an entity id to the entry whose totals actually carry its ask:
	// itself, or the entry it is a duplicate of. A link is only called a second
	// statement of an ask that is in the totals once already. An entity that
	// could not be read gets no entry here, so a link raised against it stays
	// countable on its own, which is the rule the unreadable-invoice case below
	// follows for the same reason.
	askOf := make(map[string]string)
	add := func(entry SnapshotEntry) {
		key := entry.Kind + "|" + entry.ID
		if entry.ID == "" || seen[key] {
			return
		}
		seen[key] = true
		if entry.Error == "" {
			carrier := entry.ID
			if entry.DuplicateOf != "" {
				carrier = entry.DuplicateOf
			}
			askOf[entry.ID] = carrier
		}
		snapshot.Entries = append(snapshot.Entries, entry)
	}

	skip := func(kind, customerID, reason string) {
		snapshot.Skipped = append(snapshot.Skipped, SkippedItem{Kind: kind, CustomerID: customerID, Reason: reason})
	}

	for _, item := range opts.Manifest.Items {
		if item.ID == "" {
			skip(string(item.Kind), item.CustomerID, SkipNoID)
			continue
		}
		switch item.Kind {
		case seed.EntityInvoice:
			invoice := fetchInvoiceEntry(ctx, api, item.ID)
			add(invoice)

			// The order the invoice minted. Razorpay's own answer for which
			// order that is comes off the invoice read; the manifest's copy is
			// the fallback for an invoice that could not be read.
			orderID := invoice.OrderID
			if orderID == "" {
				orderID = item.OrderID
			}
			if orderID == "" {
				break
			}
			order := fetchOrderEntry(ctx, api, orderID)
			// Only when the invoice read succeeded, because only then are the
			// invoice's amounts in the totals. An unreadable invoice
			// contributes nothing, so calling its order a duplicate of it would
			// drop the debt from the snapshot entirely.
			if invoice.Error == "" {
				order.DuplicateOf = invoice.ID
			}
			add(order)
		case seed.EntityOrder:
			add(fetchOrderEntry(ctx, api, item.ID))
		default:
			skip(string(item.Kind), item.CustomerID, SkipUnknownKind)
		}
	}
	for _, link := range slices.SortedFunc(slices.Values(opts.PaymentLinks), func(a, b MintedLink) int {
		return cmp.Compare(a.ID, b.ID)
	}) {
		entry := fetchPaymentLinkEntry(ctx, api, link.ID)
		// Only when the link itself was read and the debt it restates is in the
		// totals. A link raised against an entity that could not be read is the
		// only statement of that ask this snapshot has, so it counts on its own
		// rather than pointing at nothing.
		if entry.Error == "" && link.DebtID != "" {
			if carrier := askOf[link.DebtID]; carrier != "" {
				entry.DuplicateAskOf = carrier
			}
		}
		add(entry)
	}

	snapshot.Totals.Skipped = len(snapshot.Skipped)
	for _, entry := range snapshot.Entries {
		snapshot.Totals.Entries++
		if entry.Error != "" {
			snapshot.Totals.Errors++
			continue
		}
		if entry.DuplicateOf != "" {
			snapshot.Totals.Duplicates++
			continue
		}
		if entry.DuplicateAskOf != "" {
			// The ask is already in the gross and the due under another id. The
			// paid figure is not, because this surface does not mirror that one.
			snapshot.Totals.DuplicateAsks++
			snapshot.Totals.AmountPaidPaise += entry.AmountPaidPaise
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
	// EntriesDeduped is how many compared entries carried DuplicateOf, so their
	// amounts were left out of the two money figures above. Their status
	// changes are still in StatusChanges.
	EntriesDeduped int `json:"entries_deduped"`
	// EntriesAskDeduped is how many compared entries carried DuplicateAskOf, so
	// their amount-due change was left out and their paid change was counted.
	// See SnapshotEntry.DuplicateAskOf for why one exclusion and not both.
	EntriesAskDeduped int `json:"entries_ask_deduped"`
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
//
// An entry marked DuplicateOf is compared for its status but not for its money.
// An issued invoice and the order it minted both report the same payment, so
// summing both reports one customer paying one debt at twice its value. The
// debt is counted on the invoice, which is the entry that also carries the
// notification-status fields, and the order's status flip is still reported.
//
// An entry marked DuplicateAskOf is compared for its status, for its payment,
// and not for its amount due. A run-minted payment link restates an ask that
// another entry carries, so counting the due change on both would double it,
// but the two do not mirror each other on payment: a customer who pays the link
// does not mark the order paid, so the link's paid figure is the only record
// that the money arrived and dropping it would report a real payment as no
// recovery.
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
		// A duplicate is compared for its status and not for its money. Either
		// snapshot carrying the marker is enough: an entry that became a
		// duplicate between two reads is one whose debt the invoice now carries,
		// and counting it on the read where the marker is missing would count
		// the debt twice again.
		switch {
		case entry.DuplicateOf != "" || earlier.DuplicateOf != "":
			delta.EntriesDeduped++
		case entry.DuplicateAskOf != "" || earlier.DuplicateAskOf != "":
			// A duplicated ask is not a duplicated payment. This surface does
			// not mirror the one that carries the ask, so a payment made here
			// is visible nowhere else and it is the whole of what moved.
			delta.EntriesAskDeduped++
			delta.RecoveredPaise += entry.AmountPaidPaise - earlier.AmountPaidPaise
		default:
			delta.RecoveredPaise += entry.AmountPaidPaise - earlier.AmountPaidPaise
			delta.AmountDueChange += entry.AmountDuePaise - earlier.AmountDuePaise
		}
		if earlier.Status != entry.Status {
			delta.StatusChanges = append(delta.StatusChanges,
				fmt.Sprintf("%s: %s -> %s", entry.ID, earlier.Status, entry.Status))
		}
	}
	return delta
}
