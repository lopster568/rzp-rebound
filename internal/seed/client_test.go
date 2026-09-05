package seed

import "github.com/lopster568/rzp-recovery-agent/internal/razorpay"

// var _ Client = (*razorpay.Client)(nil) is the compile-time proof that the
// narrow interface ExecutePlan takes is actually satisfied by the concrete
// client this package is built to run against. If invoices.go or client.go
// ever renames or drops one of these four methods, this line fails to
// compile before anything else in the suite runs.
var _ Client = (*razorpay.Client)(nil)
