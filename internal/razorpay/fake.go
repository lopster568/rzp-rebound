package razorpay

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/testcards"
)

// idAlphabet is what Razorpay identifiers are made of after the prefix.
const idAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// idLength is how many characters follow the prefix. Real Razorpay ids are
// this long; nothing depends on the number beyond keeping them distinct.
const idLength = 14

// FakeOptions configures a Fake.
type FakeOptions struct {
	// Seed drives every generated identifier. Two fakes built with the same
	// seed produce the same ids for the same call sequence.
	Seed int64
	// Clock supplies created_at timestamps. Nil means the wall clock, which
	// makes the fake non-reproducible, so tests pass a fake clock.
	Clock clock.Clock
	// Cards is the test-card table. Nil means testcards.Default.
	Cards *testcards.Table
}

// Fake is an in-memory Razorpay gateway. Orders, payments, and payment links
// live in maps, and a payment outcome is decided by the card number through
// the shared testcards table. It makes no network calls and needs no keys.
type Fake struct {
	mu     sync.Mutex
	rng    *rand.Rand
	clock  clock.Clock
	cards  *testcards.Table
	orders map[string]*Order
	// paymentsByOrder keeps attempts in the order they were made.
	paymentsByOrder map[string][]string
	payments        map[string]*Payment
	links           map[string]*PaymentLink
}

var _ Port = (*Fake)(nil)

// NewFake returns a Fake.
func NewFake(opts FakeOptions) (*Fake, error) {
	cards := opts.Cards
	if cards == nil {
		var err error
		cards, err = testcards.Default()
		if err != nil {
			return nil, fmt.Errorf("razorpay: fake needs a card table: %w", err)
		}
	}

	c := opts.Clock
	if c == nil {
		c = clock.Real()
	}

	return &Fake{
		rng:             rand.New(rand.NewSource(opts.Seed)),
		clock:           c,
		cards:           cards,
		orders:          make(map[string]*Order),
		paymentsByOrder: make(map[string][]string),
		payments:        make(map[string]*Payment),
		links:           make(map[string]*PaymentLink),
	}, nil
}

// Cards returns the test-card table the fake decides outcomes from.
func (f *Fake) Cards() *testcards.Table { return f.cards }

// newID builds a deterministic identifier. The caller holds the lock, because
// the sequence the rng is read in is what makes the fake reproducible.
func (f *Fake) newID(prefix string) string {
	b := make([]byte, idLength)
	for i := range b {
		b[i] = idAlphabet[f.rng.Intn(len(idAlphabet))]
	}
	return prefix + string(b)
}

// CreateOrder records a new order in status created.
func (f *Fake) CreateOrder(_ context.Context, req CreateOrderRequest) (Order, error) {
	if req.AmountPaise <= 0 {
		return Order{}, fmt.Errorf("%w: got %d paise", ErrAmountNotPositive, req.AmountPaise)
	}

	currency := req.Currency
	if currency == "" {
		currency = "INR"
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	order := &Order{
		ID:          f.newID("order_"),
		AmountPaise: req.AmountPaise,
		AmountPaid:  0,
		AmountDue:   req.AmountPaise,
		Currency:    currency,
		Receipt:     req.Receipt,
		Status:      OrderStatusCreated,
		CreatedAt:   f.clock.Now().Unix(),
		Notes:       req.Notes,
	}
	f.orders[order.ID] = order
	return *order, nil
}

// FetchOrder returns a recorded order.
func (f *Fake) FetchOrder(_ context.Context, orderID string) (Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	order, ok := f.orders[orderID]
	if !ok {
		return Order{}, fmt.Errorf("%w: %s", ErrOrderNotFound, orderID)
	}
	return *order, nil
}

// ListPaymentsForOrder returns every attempt made on an order, oldest first.
func (f *Fake) ListPaymentsForOrder(_ context.Context, orderID string) ([]Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.orders[orderID]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrOrderNotFound, orderID)
	}

	ids := f.paymentsByOrder[orderID]
	out := make([]Payment, 0, len(ids))
	for _, id := range ids {
		out = append(out, *f.payments[id])
	}
	return out, nil
}

// FetchPayment returns a recorded payment.
func (f *Fake) FetchPayment(_ context.Context, paymentID string) (Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	payment, ok := f.payments[paymentID]
	if !ok {
		return Payment{}, fmt.Errorf("%w: %s", ErrPaymentNotFound, paymentID)
	}
	return *payment, nil
}

// CreatePaymentLink records a new payment link.
func (f *Fake) CreatePaymentLink(_ context.Context, req CreatePaymentLinkRequest) (PaymentLink, error) {
	if req.AmountPaise <= 0 {
		return PaymentLink{}, fmt.Errorf("%w: got %d paise", ErrAmountNotPositive, req.AmountPaise)
	}

	currency := req.Currency
	if currency == "" {
		currency = "INR"
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	link := &PaymentLink{
		ID:          f.newID("plink_"),
		Status:      PaymentLinkStatusCreated,
		AmountPaise: req.AmountPaise,
		Currency:    currency,
		ReferenceID: req.ReferenceID,
		CreatedAt:   f.clock.Now().Unix(),
	}
	// The host is a reserved name that resolves nowhere. The real short-url
	// shape comes from a phase 1 fixture; guessing it would put a made-up
	// fact in the repository.
	link.ShortURL = "https://pay.invalid/" + link.ID
	f.links[link.ID] = link
	return *link, nil
}

// ResendPaymentLinkNotification records that a resend was requested and
// reports that the call succeeded. It says nothing about a person reading
// anything, and neither does anything downstream of it.
func (f *Fake) ResendPaymentLinkNotification(_ context.Context, linkID, medium string) (NotifyReceipt, error) {
	if medium != MediumSMS && medium != MediumEmail {
		return NotifyReceipt{}, fmt.Errorf("%w: %q", ErrUnsupportedMedium, medium)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.links[linkID]; !ok {
		return NotifyReceipt{}, fmt.Errorf("%w: %s", ErrPaymentLinkNotFound, linkID)
	}

	return NotifyReceipt{
		LinkID:      linkID,
		Medium:      medium,
		Accepted:    true,
		RequestedAt: f.clock.Now(),
	}, nil
}

// AttemptPayment drives one payment attempt on an order with a card number.
// It is not part of Port: the live API has no equivalent, because a real
// attempt happens in checkout. Phase 1 supplies the same behaviour for the
// live client through the contract harness.
func (f *Fake) AttemptPayment(_ context.Context, orderID, cardNumber string) (Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	order, ok := f.orders[orderID]
	if !ok {
		return Payment{}, fmt.Errorf("%w: %s", ErrOrderNotFound, orderID)
	}
	if order.Status == OrderStatusPaid {
		return Payment{}, fmt.Errorf("%w: %s", ErrOrderAlreadyPaid, orderID)
	}

	success := cardNumber == f.cards.SuccessCard()
	card, documented := f.cards.FailureFor(cardNumber)
	if !success && !documented {
		return Payment{}, fmt.Errorf("%w: %s", ErrUnknownCard, cardNumber)
	}

	payment := &Payment{
		ID:          f.newID("pay_"),
		OrderID:     orderID,
		AmountPaise: order.AmountPaise,
		Currency:    order.Currency,
		Method:      "card",
		CreatedAt:   f.clock.Now().Unix(),
	}

	if success {
		payment.Status = PaymentStatusCaptured
		order.Status = OrderStatusPaid
		order.AmountPaid = order.AmountPaise
		order.AmountDue = 0
	} else {
		payment.Status = PaymentStatusFailed
		// PRD Q4 is settled. A real failed payment carries the coarse class in
		// error.code and the specific reason in error.reason, observed on
		// 2026-08-31, so the fake fills them the same way round. It used to
		// put the reason string in both, because which field carried it was
		// the open question, and a fake that answers a settled question the
		// wrong way teaches the wrong shape to every offline test on it.
		//
		// The reason string here is the documented one from
		// magic_cards.json, which live test mode does not actually produce:
		// every card came back as ReasonPaymentFailed. The fake keeps the
		// documented reasons on purpose, because it is what gives the
		// classifier's six classes anything to be exercised against.
		// DECISIONS.md has the entry.
		payment.ErrorCode = ErrorClassBadRequest
		payment.ErrorReason = card.ErrorCode
		payment.ErrorSource = ErrorSourceGateway
		payment.ErrorStep = ErrorStepPaymentAuthorization
		payment.ErrorDescription = "seeded by the fake gateway from " + testcards.DefaultPath
		order.Status = OrderStatusAttempted
	}

	order.Attempts++
	f.payments[payment.ID] = payment
	f.paymentsByOrder[orderID] = append(f.paymentsByOrder[orderID], payment.ID)
	return *payment, nil
}
