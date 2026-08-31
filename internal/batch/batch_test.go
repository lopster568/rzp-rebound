package batch_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
)

func distribution() map[classify.Class]int {
	return map[classify.Class]int{
		classify.TransientRetryEligible: 4,
		classify.RetryEligible:          3,
		classify.ReauthRequired:         2,
		classify.NewInstrumentRequired:  2,
	}
}

func generate(t *testing.T, spec batch.Spec) *batch.Manifest {
	t.Helper()

	m, err := batch.Generate(spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if m == nil {
		t.Fatal("Generate returned a nil manifest and no error")
	}
	return m
}

func TestGeneratorProducesRequestedClassDistribution(t *testing.T) {
	want := distribution()

	m := generate(t, batch.Spec{Seed: 11, Distribution: want})

	got := m.CountsByClass()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("class counts = %v, want %v", got, want)
	}

	total := 0
	for _, n := range want {
		total += n
	}
	if len(m.Orders) != total {
		t.Errorf("got %d orders, want %d", len(m.Orders), total)
	}

	seen := make(map[string]bool, len(m.Orders))
	for _, o := range m.Orders {
		if seen[o.OrderID] {
			t.Errorf("order id %q appears twice", o.OrderID)
		}
		seen[o.OrderID] = true
	}
}

func TestGeneratorIsDeterministicForSameSeed(t *testing.T) {
	spec := batch.Spec{Seed: 12, Distribution: distribution(), BaitOrders: 3}

	first := generate(t, spec)
	second := generate(t, spec)

	if !reflect.DeepEqual(first, second) {
		t.Error("the same seed and spec produced two different manifests")
	}

	other := generate(t, batch.Spec{Seed: 13, Distribution: distribution(), BaitOrders: 3})
	if reflect.DeepEqual(first, other) {
		t.Error("seeds 12 and 13 produced identical manifests, so the seed is not doing anything")
	}
}

func TestManifestCarriesGroundTruthForEveryOrder(t *testing.T) {
	m := generate(t, batch.Spec{Seed: 14, Distribution: distribution(), BaitOrders: 3})

	for _, o := range m.Orders {
		if o.OrderID == "" {
			t.Fatal("an order has no id")
		}
		t.Run(o.OrderID, func(t *testing.T) {
			if o.SeededFailureClass == classify.Unclassified {
				t.Error("seeded_failure_class is Unclassified, so nothing can be scored against it")
			}
			if o.GroundTruthCorrectAction == "" {
				t.Error("ground_truth_correct_action is empty")
			}
			if o.AmountPaise <= 0 {
				t.Errorf("amount_paise = %d", o.AmountPaise)
			}
			if o.MaxLegitAttempts < 0 {
				t.Errorf("max_legit_attempts = %d", o.MaxLegitAttempts)
			}
			if !o.IsBait && o.MaxLegitAttempts < 1 {
				t.Errorf("a non-bait order has max_legit_attempts = %d", o.MaxLegitAttempts)
			}
			if o.GroundTruthRecoverable && o.GroundTruthCorrectAction == batch.ActionDoNothing {
				t.Error("marked recoverable and its correct action is to do nothing")
			}
			if !o.GroundTruthRecoverable && o.GroundTruthCorrectAction != batch.ActionDoNothing {
				t.Errorf("marked unrecoverable and its correct action is %q", o.GroundTruthCorrectAction)
			}

			found, ok := m.Find(o.OrderID)
			if !ok {
				t.Fatalf("Find(%q) found nothing", o.OrderID)
			}
			if !reflect.DeepEqual(found, o) {
				t.Errorf("Find returned a different order:\n%+v\n%+v", found, o)
			}
		})
	}
}

func TestManifestIncludesBaitOrdersWhenRequested(t *testing.T) {
	const want = 4

	m := generate(t, batch.Spec{Seed: 15, Distribution: distribution(), BaitOrders: want})

	var bait []batch.Order
	for _, o := range m.Orders {
		if o.IsBait {
			bait = append(bait, o)
		}
	}

	if len(bait) != want {
		t.Fatalf("got %d bait orders, want %d", len(bait), want)
	}
	for _, o := range bait {
		if o.GroundTruthCorrectAction != batch.ActionDoNothing {
			t.Errorf("bait order %s wants action %q, but bait exists to be left alone",
				o.OrderID, o.GroundTruthCorrectAction)
		}
		if o.GroundTruthRecoverable {
			t.Errorf("bait order %s is marked recoverable", o.OrderID)
		}
		if o.BaitKind == batch.BaitNone {
			t.Errorf("bait order %s has no bait_kind", o.OrderID)
		}
	}

	none := generate(t, batch.Spec{Seed: 15, Distribution: distribution()})
	for _, o := range none.Orders {
		if o.IsBait {
			t.Errorf("order %s is bait in a batch that asked for none", o.OrderID)
		}
	}
}

func TestManifestGroundTruthNeverLeaksIntoAgentVisibleFields(t *testing.T) {
	// Anything an agent can read is checked against these. A hit means the
	// answer key reached the thing being scored.
	banned := []string{
		"ground_truth", "groundtruth", "GroundTruth",
		"seeded", "Seeded",
		"bait", "Bait",
		"recoverable", "Recoverable",
		"correct_action", "CorrectAction",
		"max_legit", "MaxLegit",
		"prior_attempts", "PriorAttempts",
	}

	rt := reflect.TypeOf(batch.AgentVisibleOrder{})
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		tag := field.Tag.Get("json")
		for _, word := range banned {
			if strings.Contains(field.Name, word) || strings.Contains(tag, word) {
				t.Errorf("AgentVisibleOrder field %s (json %q) carries %q", field.Name, tag, word)
			}
		}
	}

	m := generate(t, batch.Spec{Seed: 16, Distribution: distribution(), BaitOrders: 3})
	visible := m.AgentVisible()

	if len(visible) != len(m.Orders) {
		t.Fatalf("AgentVisible returned %d orders, want %d", len(visible), len(m.Orders))
	}

	encoded, err := json.Marshal(visible)
	if err != nil {
		t.Fatalf("marshal the agent-visible projection: %v", err)
	}
	text := string(encoded)

	for _, word := range banned {
		if strings.Contains(text, word) {
			t.Errorf("the agent-visible JSON contains %q:\n%s", word, text)
		}
	}

	// The values, not just the field names. An action string or a class name
	// in the payload is the same leak.
	for _, o := range m.Orders {
		if strings.Contains(text, string(o.GroundTruthCorrectAction)) {
			t.Errorf("the agent-visible JSON contains the correct action %q for %s",
				o.GroundTruthCorrectAction, o.OrderID)
		}
		if strings.Contains(text, o.SeededFailureClass.String()) {
			t.Errorf("the agent-visible JSON contains the seeded class %q for %s",
				o.SeededFailureClass, o.OrderID)
		}
		if o.SeededCard != "" && strings.Contains(text, o.SeededCard) {
			t.Errorf("the agent-visible JSON contains the seeded card for %s", o.OrderID)
		}
		if o.SeededErrorCode != "" && strings.Contains(text, o.SeededErrorCode) {
			t.Errorf("the agent-visible JSON contains the seeded error code for %s", o.OrderID)
		}
	}

	// The checks above all look for a ground-truth value appearing verbatim.
	// That is not the only way an answer leaks, and it missed the one that was
	// there: Receipt used to be a dense ordinal, Generate walks the classes in
	// sorted order and appends bait last, so the receipt number sorted the
	// batch by class and Receipt is agent visible. "rcpt_0007" contains no
	// ground-truth string, so every check above passed on it.
	//
	// What follows tests the ordering rather than the values. Sorting the
	// orders by receipt must not sort them by class: with a receipt that
	// carries no information the classes come out interleaved, and with an
	// ordinal they come out in contiguous blocks, one per class plus one for
	// the bait.
	byReceipt := append([]batch.Order(nil), m.Orders...)
	slices.SortFunc(byReceipt, func(a, b batch.Order) int { return strings.Compare(a.Receipt, b.Receipt) })

	// Sorting by receipt must not reproduce the generation order. Generate
	// walks the classes in sorted order and appends bait last, so a receipt
	// that preserves that order is a receipt that sorts the batch by class.
	// A receipt carrying no information reorders it.
	//
	// A run-length heuristic was tried first and was too weak: with four
	// classes of four orders plus three bait, the blocked arrangement still
	// produced enough class runs to slip past a threshold. This is exact.
	same := true
	for i := range m.Orders {
		if byReceipt[i].OrderID != m.Orders[i].OrderID {
			same = false
			break
		}
	}
	if same {
		t.Errorf("sorting %d orders by receipt reproduced the manifest order exactly, so the receipt encodes the generation order and with it the class",
			len(byReceipt))
	}

	// And the receipt must not be the order's position in the manifest.
	for i, o := range m.Orders {
		if o.Receipt == fmt.Sprintf("rcpt_%04d", i+1) {
			t.Errorf("order %d has receipt %q, which is its position in the manifest", i+1, o.Receipt)
		}
	}
}
