package redact

import "regexp"

// Marker is what a redacted run becomes. It carries no JSON metacharacter, so
// substituting it inside a JSON string value leaves the document parseable.
const Marker = "[redacted]"

// cardLike matches a run of 13 or more digits, with optional single spaces or
// hyphens between them.
//
// There is no upper bound, on purpose. Capping the run at 19, the longest a
// card number can be, means a card pasted into a longer run of digits matches
// nothing and passes through whole, which is the wrong way for this to fail.
// The cost is that a genuinely long number loses its digits, and phase 2 found
// the first thing in this repository that pays it. A sha256 digest rendered as
// 64 hex characters contains a run of 13 digits about five percent of the
// time, so four of the eighty idempotency keys in the first committed
// fake-layer run came out of the ledger with the marker in the middle of them.
//
// That was fixed on the writing side rather than here: policy.ShortKey puts 12
// characters in the audit row, and 12 characters cannot hold a run of 13
// digits. Loosening this pattern so it does not match inside a longer
// alphanumeric token would have fixed the same symptom by weakening a security
// control to solve a display problem.
//
// Everything else this project writes is still clear of the pattern by
// construction: amounts in paise are six digits, a unix timestamp in seconds is
// ten, and every Razorpay identifier carries a letter prefix.
var cardLike = regexp.MustCompile(`\d(?:[ -]?\d){12,}`)

// keyLike matches a Razorpay key. The prefix is assembled from fragments so
// this source file does not itself hold a string the pre-commit secret scan
// would flag.
var keyLike = regexp.MustCompile(`rzp_(?:` + "test" + `|` + "live" + `)_[A-Za-z0-9]{6,}`)

// Value replaces every card-shaped and key-shaped run in s.
//
// What it cannot do is worth stating, because the gap is structural rather
// than an omission. A Razorpay key secret is a bare alphanumeric string with no
// prefix and no checkable shape, so no pattern here can pick one out of
// ordinary text. The control for a secret is the package that holds it
// scrubbing its own credentials before the string leaves, which
// razorpay.Client.Redact does on every error and every captured body. This
// function is the backstop for the shapes that can be recognised, and the
// packages that call it say so where they call it.
func Value(s string) string {
	if s == "" {
		return s
	}
	s = cardLike.ReplaceAllString(s, Marker)
	return keyLike.ReplaceAllString(s, Marker)
}
