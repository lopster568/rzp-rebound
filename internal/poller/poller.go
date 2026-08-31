package poller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// Defaults. Interval doubles on every poll up to MaxBackoff, and MaxWait ends
// the run whatever state the order is in.
const (
	DefaultInterval   = 500 * time.Millisecond
	DefaultMaxBackoff = 5 * time.Second
	DefaultMaxWait    = 2 * time.Minute
)

// ErrNoPort is returned when a Poller is built with no gateway behind it.
var ErrNoPort = errors.New("poller: needs a razorpay.Port")

// WaitFunc blocks for d or until ctx ends. It is the backoff seam: a test
// supplies one that records the duration and advances a fake clock, so no test
// sleeps.
type WaitFunc func(ctx context.Context, d time.Duration) error

// Options configures a Poller.
type Options struct {
	// Port is the gateway to read through. Required.
	Port razorpay.Port
	// Clock measures elapsed time. Nil means the wall clock.
	Clock clock.Clock
	// Wait is the backoff seam. Nil means a real timer.
	Wait WaitFunc
	// Interval is the first wait between polls. Zero means DefaultInterval.
	Interval time.Duration
	// MaxBackoff caps the exponential growth. Zero means DefaultMaxBackoff.
	MaxBackoff time.Duration
	// MaxWait bounds the whole run. Zero means DefaultMaxWait.
	MaxWait time.Duration
}

// Poller reads an order and its payments until the order reaches a terminal
// state or the run runs out of time.
type Poller struct {
	port       razorpay.Port
	clock      clock.Clock
	wait       WaitFunc
	interval   time.Duration
	maxBackoff time.Duration
	maxWait    time.Duration
}

// Result is what one poll run saw. It is filled in whether the run reached a
// terminal state or ran out of time, so a timeout still reports state rather
// than discarding it.
type Result struct {
	OrderID string
	// Order is the last order the poller read.
	Order razorpay.Order
	// Payments is every attempt on the order, oldest first, as of the last
	// poll.
	Payments []razorpay.Payment
	// Terminal reports that the order reached a terminal state.
	Terminal bool
	// TimedOut reports that MaxWait ran out first. Order and Payments still
	// hold the last state the poller saw.
	TimedOut bool
	// FailedPayment is the most recent failed payment on the order, or nil.
	// An order sitting at attempted with a failed payment under it is not
	// terminal: another attempt can still pay it.
	FailedPayment *razorpay.Payment
	// Polls counts how many times the gateway was read.
	Polls int
	// Waited is the total backoff the run asked for, measured on the injected
	// clock.
	Waited time.Duration
}

// IsTerminal reports whether an order status ends a poll run.
//
// Only paid does. An order at created has had no attempt yet, and an order at
// attempted has had one that failed, and either can still become paid.
func IsTerminal(status string) bool { return status == razorpay.OrderStatusPaid }

// New returns a Poller.
func New(opts Options) (*Poller, error) {
	if opts.Port == nil {
		return nil, ErrNoPort
	}

	p := &Poller{
		port:       opts.Port,
		clock:      opts.Clock,
		wait:       opts.Wait,
		interval:   opts.Interval,
		maxBackoff: opts.MaxBackoff,
		maxWait:    opts.MaxWait,
	}
	if p.clock == nil {
		p.clock = clock.Real()
	}
	if p.wait == nil {
		p.wait = sleepWait
	}
	if p.interval <= 0 {
		p.interval = DefaultInterval
	}
	if p.maxBackoff <= 0 {
		p.maxBackoff = DefaultMaxBackoff
	}
	if p.maxBackoff < p.interval {
		p.maxBackoff = p.interval
	}
	if p.maxWait <= 0 {
		p.maxWait = DefaultMaxWait
	}
	return p, nil
}

// sleepWait is the default backoff: a timer that gives up when the context
// does.
func sleepWait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// PollUntilTerminal reads the order and its payments until the order is
// terminal or MaxWait runs out.
//
// A timeout is not an error: it comes back as Result.TimedOut with the last
// state the poller saw, because a caller that has to decide what to do next
// needs that state more than it needs an error value. A gateway error is an
// error, and the partial Result comes back with it.
func (p *Poller) PollUntilTerminal(ctx context.Context, orderID string) (Result, error) {
	res := Result{OrderID: orderID}
	start := p.clock.Now()
	backoff := p.interval

	for {
		order, err := p.port.FetchOrder(ctx, orderID)
		if err != nil {
			return res, fmt.Errorf("poller: fetch %s: %w", orderID, err)
		}
		payments, err := p.port.ListPaymentsForOrder(ctx, orderID)
		if err != nil {
			return res, fmt.Errorf("poller: list payments for %s: %w", orderID, err)
		}

		res.Polls++
		res.Order = order
		res.Payments = payments
		res.FailedPayment = lastFailed(payments)

		if IsTerminal(order.Status) {
			res.Terminal = true
			return res, nil
		}

		// Stop before a wait that would run past the budget, rather than
		// after it. Sleeping past a deadline and then noticing is a slower way
		// to reach the same answer.
		if p.clock.Now().Sub(start)+backoff > p.maxWait {
			res.TimedOut = true
			return res, nil
		}

		if err := p.wait(ctx, backoff); err != nil {
			return res, fmt.Errorf("poller: backoff on %s: %w", orderID, err)
		}
		res.Waited += backoff
		backoff = min(backoff*2, p.maxBackoff)
	}
}

// lastFailed returns the most recent failed payment, or nil. Payments arrive
// oldest first, so the last failure is the one a retry decision is made from.
func lastFailed(payments []razorpay.Payment) *razorpay.Payment {
	for i := len(payments) - 1; i >= 0; i-- {
		if payments[i].Status == razorpay.PaymentStatusFailed {
			found := payments[i]
			return &found
		}
	}
	return nil
}
