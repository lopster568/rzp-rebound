package policy

// Deprecated shims from the retry engine.
//
// Nothing in this file is part of the policy. It exists so that packages
// another work package owns, which still spell an action "retry_same_instrument"
// and a cap "max attempts", keep compiling while they are ported. Every
// identifier here is unreferenced by the engine itself.
//
// The action strings below are deliberately not added to lawfulActions, and
// there is no rule branch that reads any of them. A caller that hands one to
// Evaluate reaches R7 and gets an escalation naming the action, which is the
// behaviour a half-ported caller should have: visible in the audit trail,
// refusing to act, and impossible to mistake for an allow.
//
// Delete this file when internal/recovery, internal/mcpserver, internal/store,
// and cmd/rzp no longer reference it.

// The retry engine's action vocabulary.
//
// Deprecated: use the lawful action set in sources.go. Unattended
// re-presentment of a one-off Indian payment has no lawful counterpart on any
// rail, which is why these are strings with no rule behind them rather than
// actions.
const (
	ActionNone                 = "none"
	ActionRetrySameInstrument  = "retry_same_instrument"
	ActionRequestReauth        = "request_reauth"
	ActionRequestNewInstrument = "request_new_instrument"
)

// DefaultMaxAttemptsPerOrder is the old name for the contact cap.
//
// Deprecated: use DefaultMaxTouchesPerItem, or read the per-source cap from
// SourceParams. The number is the same and the concept is not: an attempt was
// a re-presentment of a payment, a touch is an outbound message.
const DefaultMaxAttemptsPerOrder = DefaultMaxTouchesPerItem
