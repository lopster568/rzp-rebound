package intervene

import "context"

// contextKey is unexported so nothing outside this package can collide with
// the two values below.
type contextKey int

const (
	idempotencyKeyContextKey contextKey = iota
	reasonContextKey
)

// WithIdempotencyKey puts key on ctx for the next Apply.
//
// riskitem.Intervention.Apply is frozen at three parameters, so the key
// travels on the context rather than in the signature. The policy engine is
// what computes it: this package only reads it, and treats two calls carrying
// one key as one decision.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyContextKey, key)
}

// IdempotencyKey returns the key on ctx, empty when there is none.
func IdempotencyKey(ctx context.Context) string {
	key, _ := ctx.Value(idempotencyKeyContextKey).(string)
	return key
}

// WithReason puts a human-readable reason on ctx.
//
// It is what the escalation record and the promise ledger note carry, and it
// is the one piece of context that cannot be derived from the item: why the
// policy gate chose this action. Without it both fall back to a generated
// line naming the item and the action, which is true and says less.
func WithReason(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, reasonContextKey, reason)
}

// Reason returns the reason on ctx, empty when there is none.
func Reason(ctx context.Context) string {
	reason, _ := ctx.Value(reasonContextKey).(string)
	return reason
}
