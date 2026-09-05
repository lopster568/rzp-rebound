// Package riskrun is the pipeline: three detectors, one dedupe, the policy
// gate, and the intervention engine, over the ground truth a seedbook manifest
// records.
//
// It owns the wiring and nothing else. Every judgment it makes is deferred to
// the package that owns it: internal/detect finds the debt, internal/classify
// reads a failure, internal/policy decides, internal/intervene acts, and
// internal/audit records. What is here is the order those run in, the facts the
// item itself cannot carry, the two arms, and the two files a run leaves
// behind.
//
// Two arms, assigned per item. a0-control detects, classifies, and evaluates,
// and then stops: the verdict is written down and nothing is executed. a1-engine
// executes what the gate allowed. Assignment is a seeded shuffle stratified by
// source, so the two arms see the same mix of failed payments, unpaid orders,
// and overdue invoices rather than one arm getting all the invoices.
//
// The disputed flag is the one fact here that comes from the manifest rather
// than from Razorpay. Razorpay has no field for a contested debt, so the MCP
// server documents that R13 cannot fire through it; this package reads
// seed.Flags.Disputed off the manifest that seeded the item and hands it to the
// gate, which is why R13 can fire in a run driven from here.
//
// The dry-run path makes no network call of any kind. It builds the Razorpay
// entities the detectors read from the manifest itself, runs the real
// detectors, the real dedupe, and the real gate over them, and stops before the
// intervention engine. What it proves is that detect, collapse, and policy
// agree end to end; what it cannot prove is anything about Razorpay's answers.
package riskrun
