// Package seed builds and executes a live Razorpay test-mode seeding plan for
// the Unified Revenue-at-Risk Engine demo, and writes the manifest that is
// the eval scorer's ground truth for it.
//
// Plan generation (GeneratePlan) touches no network at all and is what the
// unit tests exercise. ExecutePlan is the only part of this package that
// calls Razorpay, and it does so through the narrow Client interface rather
// than the concrete *razorpay.Client, so a test can stub it and no test here
// spends a real API call.
//
// One of the three risk-item classes, failed payments, cannot be created
// through the API at all: test-mode checkout is browser-only, and the
// headless attempt path this repository otherwise drives payments through
// answers 403 for a plain failed-payment seed. This package seeds the other
// two classes it can reach directly (overdue invoices and abandoned orders)
// and, in the manifest's Instructions field, tells the operator which links
// to open and which documented test-mode cards to fail them with by hand, so
// the failed-payment detector has real data to read on its next sweep.
//
// Aging is a manifest property, not an API one. Nothing in the invoice or
// order creation calls lets a caller backdate issued_at or created_at, so
// every item this package creates is, to Razorpay, brand new. The intended
// age (fresh, 30, 60, or 90 days overdue) is recorded only in the manifest,
// as AgeBucket and SimulatedAtRiskSince, and the detector and scorer this
// feeds are expected to read that field instead of doing arithmetic on the
// timestamp Razorpay actually reports.
package seed
