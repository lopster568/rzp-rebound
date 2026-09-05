package riskrun

import (
	"maps"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// items builds n sightings of one source, with ids that differ.
func items(source riskitem.Source, n int) []riskitem.RiskItem {
	out := make([]riskitem.RiskItem, n)
	for i := range out {
		id := string(source) + "-" + string(rune('a'+i))
		out[i] = riskitem.RiskItem{
			ID:       riskitem.NewID(source, id),
			Source:   source,
			SourceID: id,
		}
	}
	return out
}

// TestAssignArmsSplitsEverySourceRatherThanTheQueue is the stratification.
//
// An unstratified shuffle over a queue of seven invoices and three orders can
// put every order in one arm, and the two arms would then differ by which debts
// they saw rather than by what they did with them. The split is per source, so
// each source's items divide as evenly as its count allows.
func TestAssignArmsSplitsEverySourceRatherThanTheQueue(t *testing.T) {
	queue := append(items(riskitem.SourceOverdueInvoice, 7), items(riskitem.SourceUnpaidOrder, 3)...)
	queue = append(queue, items(riskitem.SourceFailedPayment, 4)...)

	for seed := int64(0); seed < 50; seed++ {
		assignment := AssignArms(queue, seed)
		if len(assignment) != len(queue) {
			t.Fatalf("seed %d assigned %d of %d items", seed, len(assignment), len(queue))
		}

		counts := map[riskitem.Source]map[string]int{}
		for _, item := range queue {
			arm := assignment[item.ID]
			if !IsArm(arm) {
				t.Fatalf("seed %d put %s in %q, which is not an arm", seed, item.ID, arm)
			}
			if counts[item.Source] == nil {
				counts[item.Source] = map[string]int{}
			}
			counts[item.Source][arm]++
		}

		for source, byArm := range counts {
			engine, control := byArm[ArmEngine], byArm[ArmControl]
			if engine-control != 1 && engine-control != 0 {
				t.Errorf("seed %d split %s %d engine to %d control, want a difference of at most one with the extra in the engine arm",
					seed, source, engine, control)
			}
		}
	}
}

// TestAssignArmsIsDeterministicInTheSeed is what lets a run be repeated: the
// same seed over the same sightings assigns the same arms, and a different seed
// eventually assigns different ones.
func TestAssignArmsIsDeterministicInTheSeed(t *testing.T) {
	queue := append(items(riskitem.SourceOverdueInvoice, 6), items(riskitem.SourceUnpaidOrder, 5)...)

	first := AssignArms(queue, 1234)
	if !maps.Equal(first, AssignArms(queue, 1234)) {
		t.Error("one seed assigned two different sets of arms")
	}

	var differed bool
	for seed := int64(0); seed < 20 && !differed; seed++ {
		differed = !maps.Equal(first, AssignArms(queue, seed))
	}
	if !differed {
		t.Error("twenty seeds all produced the same assignment, so the seed is not being read")
	}
}

// TestAssignArmsHandlesTheDegenerateSizes. A source with one item goes to the
// arm that acts, and an empty queue assigns nothing rather than panicking.
func TestAssignArmsHandlesTheDegenerateSizes(t *testing.T) {
	if got := AssignArms(nil, 1); len(got) != 0 {
		t.Errorf("an empty queue assigned %d arm(s)", len(got))
	}

	one := items(riskitem.SourceOverdueInvoice, 1)
	if got := AssignArms(one, 7)[one[0].ID]; got != ArmEngine {
		t.Errorf("a lone item went to %q, want %q", got, ArmEngine)
	}
}

// TestProposeActionReadsTheItemShapeAndNotItsChannels pins both halves of the
// proposal, including the half that looks like a bug and is not: an item with
// nowhere to send is still proposed a notification, so that R10 is what refuses
// it and the refusal is in the trail under the rule written for it.
func TestProposeActionReadsTheItemShapeAndNotItsChannels(t *testing.T) {
	tests := []struct {
		name string
		item riskitem.RiskItem
		want string
	}{
		{
			name: "no handle means there is nothing to pay against yet",
			item: riskitem.RiskItem{Customer: riskitem.Customer{Email: "a@example.com"}},
			want: riskitem.ActionCreatePaymentLink,
		},
		{
			name: "an invoice handle and an address",
			item: riskitem.RiskItem{
				PayHandle: riskitem.PayHandle{Kind: riskitem.HandleKindInvoice, ID: "inv_1"},
				Customer:  riskitem.Customer{Email: "a@example.com", Contact: "9000090000"},
			},
			want: riskitem.ActionNotifyEmail,
		},
		{
			name: "a handle and a phone number only",
			item: riskitem.RiskItem{
				PayHandle: riskitem.PayHandle{Kind: riskitem.HandleKindPaymentLink, ID: "plink_1"},
				Customer:  riskitem.Customer{Contact: "9000090000"},
			},
			want: riskitem.ActionNotifySMS,
		},
		{
			name: "a handle and no channel at all, which R10 refuses rather than this",
			item: riskitem.RiskItem{PayHandle: riskitem.PayHandle{Kind: riskitem.HandleKindInvoice, ID: "inv_2"}},
			want: riskitem.ActionNotifySMS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProposeAction(tt.item)
			if got != tt.want {
				t.Errorf("ProposeAction = %q, want %q", got, tt.want)
			}
			if !riskitem.IsLawfulAction(got) {
				t.Errorf("ProposeAction returned %q, which is not in the lawful set", got)
			}
		})
	}
}
