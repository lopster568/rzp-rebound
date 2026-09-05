package policy

import (
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// This file is the only one in the package that imports the frozen contract.
//
// The engine in policy.go reads a plain Request whose fields it mirrors, which
// keeps Evaluate drivable from a table in a test with no fixtures and keeps
// the rules from growing an opinion about where an item came from. There is no
// import cycle to avoid here, unlike the recovery package the old policy
// redeclared constants for: riskitem imports nothing outside the standard
// library. The separation is for the shape of the tests and for the blast
// radius of a contract change, not for the compiler.
//
// The cost of mirroring is that the two vocabularies can drift.
// TestPolicyAndRiskItemShareOneSourceVocabulary and
// TestPolicyAndRiskItemShareOneActionSurface are what stop that.

// Facts is what the caller knows about an item that the item itself does not
// carry.
//
// Every field here comes from somewhere outside the detector's sighting: the
// action ledger knows which touch this is, the promise ledger knows about a
// hold, a dispute is a thing a person recorded, and the source resource's
// status is read separately from the item. A detector cannot fill any of them
// in, which is why they are not on riskitem.RiskItem.
type Facts struct {
	// TouchNo is which outbound contact this action would be, counting from 1.
	TouchNo int
	// PromiseHoldUntil is when an active promise expires, from
	// promise.Store.ActiveHold. Zero means no promise is holding.
	PromiseHoldUntil time.Time
	// Disputed reports that somebody has contested this debt.
	Disputed bool
	// SourceStatus is the Razorpay status of the resource behind the item,
	// such as cancelled or expired.
	SourceStatus string
}

// RequestFrom builds the engine's Request from an item, a proposed action, and
// the facts the item does not carry.
//
// It copies and derives nothing else. In particular it does not classify: the
// caller passes the class it read from internal/classify, because a policy
// that classified would be making the judgment it is supposed to be gating.
// Use RequestFromClassified when the class is already known, or this function
// with classify.Unclassified when it is not and let R7 escalate.
func RequestFrom(item riskitem.RiskItem, action string, facts Facts) Request {
	return RequestFromClassified(item, action, classify.Unclassified, facts)
}

// RequestFromClassified is RequestFrom with the failure class supplied.
func RequestFromClassified(item riskitem.RiskItem, action string, class classify.Class, facts Facts) Request {
	var atRisk time.Time
	if item.AtRiskSince > 0 {
		atRisk = time.Unix(item.AtRiskSince, 0).UTC()
	}

	return Request{
		RiskItemID:     item.ID,
		Source:         string(item.Source),
		Action:         action,
		Class:          class,
		SignalPresent:  HasSignal(item.Signal),
		AmountPaise:    item.AmountPaise,
		AmountDuePaise: item.AmountDuePaise,
		HasEmail:       item.Customer.Email != "",
		HasContact:     item.Customer.Contact != "",

		SourceStatus:     facts.SourceStatus,
		Disputed:         facts.Disputed,
		AtRiskSince:      atRisk,
		PromiseHoldUntil: facts.PromiseHoldUntil,
		TouchNo:          facts.TouchNo,
	}
}

// HasSignal reports whether a detector saw any failure evidence at all.
//
// It reads the two failure fields and nothing else. Method, Attempts, and the
// two notification-status fields are carried on every item that has them and
// say nothing about whether a payment failed, so counting them would make
// every unpaid order look like it had a failure signal and defeat the arm of
// R7 that separates "nothing went wrong" from "something went wrong and
// nothing could read it".
func HasSignal(s riskitem.Signal) bool {
	return s.FailureReason != "" || s.FailureCode != ""
}
