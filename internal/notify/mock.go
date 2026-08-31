package notify

import (
	"context"
	"sync"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// Send is one recorded call on a Mock.
type Send struct {
	LinkID string
	Medium string
}

// Mock is a NotifierPort that records what it was asked to send and returns
// whatever the test configured. It lives in the package rather than in a test
// file because the recovery tests need it too.
type Mock struct {
	mu    sync.Mutex
	sends []Send

	// Err, when set, comes back instead of a receipt. It is how a test drives
	// the API-failure path.
	Err error
	// Accepted is what the returned razorpay.NotifyReceipt reports. It
	// defaults to true through NewMock.
	Accepted bool
	// Clock stamps the returned receipt. Nil means the wall clock.
	Clock clock.Clock
}

var _ NotifierPort = (*Mock)(nil)

// NewMock returns a Mock that accepts every send.
func NewMock(c clock.Clock) *Mock {
	if c == nil {
		c = clock.Real()
	}
	return &Mock{Accepted: true, Clock: c}
}

// ResendPaymentLinkNotification records the call and returns the configured
// answer. The call is recorded even when Err is set, because an attempt that
// failed still happened and the audit trail says so.
func (m *Mock) ResendPaymentLinkNotification(_ context.Context, linkID, medium string) (razorpay.NotifyReceipt, error) {
	m.mu.Lock()
	m.sends = append(m.sends, Send{LinkID: linkID, Medium: medium})
	m.mu.Unlock()

	if m.Err != nil {
		return razorpay.NotifyReceipt{}, m.Err
	}

	c := m.Clock
	if c == nil {
		c = clock.Real()
	}
	return razorpay.NotifyReceipt{
		LinkID:      linkID,
		Medium:      medium,
		Accepted:    m.Accepted,
		RequestedAt: c.Now(),
	}, nil
}

// Sends returns a copy of everything the mock was asked to send, in order.
func (m *Mock) Sends() []Send {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Send(nil), m.sends...)
}
