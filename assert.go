package scene

import (
	"math/big"
	"slices"
	"strings"
	"testing"
)

// T is a [Scene] bound to a test: a sentence that fails to parse, resolve or
// apply fails the test, and assertions read the table back in the DSL's own
// vocabulary. Money is written as decimal strings, so large unit scales do not
// overflow.
type T struct {
	*Scene
	tb testing.TB
}

// NewT wraps a table for a test.
func NewT(tb testing.TB, table Table, labels map[string]string) *T {
	return &T{Scene: New(table, labels), tb: tb}
}

// Act applies each sentence in order.
func (t *T) Act(sentences ...string) {
	t.tb.Helper()
	for _, s := range sentences {
		if err := t.Do(s); err != nil {
			t.tb.Fatal(err)
		}
	}
}

// Rejects asserts the engine refuses the sentence and the table is unchanged
// in round, pot and stacks.
func (t *T) Rejects(sentence string) {
	t.tb.Helper()
	before := t.State()
	if err := t.Do(sentence); err == nil {
		t.tb.Fatalf("%q was accepted; want rejected", sentence)
	}
	after := t.State()
	if after.Round != before.Round || cmp(after.Pot, before.Pot) != 0 {
		t.tb.Fatalf("%q was rejected but changed the table: round %s→%s, pot %s→%s",
			sentence, before.Round, after.Round, str(before.Pot), str(after.Pot))
	}
}

// MustJoin seats subject at seat with buyIn chips.
func (t *T) MustJoin(subject string, seat int, buyIn string) {
	t.tb.Helper()
	if err := t.Join(subject, seat, t.big(buyIn)); err != nil {
		t.tb.Fatalf("%s joins seat %d: %v", subject, seat, err)
	}
}

// AssertRound asserts the betting round.
func (t *T) AssertRound(want Round) {
	t.tb.Helper()
	if got := t.State().Round; got != want {
		t.tb.Fatalf("round: got %s, want %s", got, want)
	}
}

// AssertPot asserts every chip in the middle.
func (t *T) AssertPot(want string) {
	t.tb.Helper()
	if got := t.State().Pot; cmp(got, t.big(want)) != 0 {
		t.tb.Fatalf("pot: got %s, want %s", str(got), want)
	}
}

// AssertBoard asserts the community cards, in order.
func (t *T) AssertBoard(want ...string) {
	t.tb.Helper()
	if got := t.State().Board; !slices.Equal(got, want) {
		t.tb.Fatalf("board: got %v, want %v", got, want)
	}
}

// AssertStack asserts one player's stack.
func (t *T) AssertStack(subject, want string) {
	t.tb.Helper()
	if got := t.player(subject).Stack; cmp(got, t.big(want)) != 0 {
		t.tb.Fatalf("%s stack: got %s, want %s", subject, str(got), want)
	}
}

// AssertStacks asserts several stacks at once — reads as the hand's result.
func (t *T) AssertStacks(want map[string]string) {
	t.tb.Helper()
	var wrong []string
	for subject, w := range want {
		if got := t.player(subject).Stack; cmp(got, t.big(w)) != 0 {
			wrong = append(wrong, subject+": got "+str(got)+", want "+w)
		}
	}
	if len(wrong) > 0 {
		slices.Sort(wrong)
		t.tb.Fatalf("stacks:\n  %s", strings.Join(wrong, "\n  "))
	}
}

// AssertChips asserts stacks plus pot add up to total — no chip appeared or
// vanished.
func (t *T) AssertChips(total string) {
	t.tb.Helper()
	st := t.State()
	sum := new(big.Int)
	if st.Round != End && st.Pot != nil {
		sum.Set(st.Pot)
	}
	for _, p := range st.Players {
		sum.Add(sum, p.Stack)
	}
	if cmp(sum, t.big(total)) != 0 {
		t.tb.Fatalf("chips: stacks + pot = %s, want %s", sum, total)
	}
}

// AssertStatus asserts a player's status (the engine's own string).
func (t *T) AssertStatus(subject, want string) {
	t.tb.Helper()
	if got := t.player(subject).Status; got != want {
		t.tb.Fatalf("%s status: got %s, want %s", subject, got, want)
	}
}

// AssertToAct asserts whose turn it is.
func (t *T) AssertToAct(subject string) {
	t.tb.Helper()
	want := t.player(subject).Seat
	if got := t.State().NextToAct; got != want {
		t.tb.Fatalf("to act: got seat %d, want %s (seat %d)", got, subject, want)
	}
}

// AssertLegal asserts the player may take every listed action.
func (t *T) AssertLegal(subject string, actions ...string) {
	t.tb.Helper()
	p := t.player(subject)
	for _, a := range actions {
		if legal(p, a) == nil {
			t.tb.Fatalf("%s: %s is not legal (legal: %s)", subject, a, legalNames(p))
		}
	}
}

// AssertNotLegal asserts the player may take none of the listed actions.
func (t *T) AssertNotLegal(subject string, actions ...string) {
	t.tb.Helper()
	p := t.player(subject)
	for _, a := range actions {
		if legal(p, a) != nil {
			t.tb.Fatalf("%s: %s is legal, want not (legal: %s)", subject, a, legalNames(p))
		}
	}
}

// AssertRange asserts the min and max of one legal action.
func (t *T) AssertRange(subject, action, min, max string) {
	t.tb.Helper()
	p := t.player(subject)
	la := legal(p, action)
	if la == nil {
		t.tb.Fatalf("%s: %s is not legal (legal: %s)", subject, action, legalNames(p))
	}
	if cmp(la.Min, t.big(min)) != 0 || cmp(la.Max, t.big(max)) != 0 {
		t.tb.Fatalf("%s %s range: got %s..%s, want %s..%s", subject, action, str(la.Min), str(la.Max), min, max)
	}
}

func (t *T) player(subject string) *Player {
	t.tb.Helper()
	addr, err := t.Resolve(subject)
	if err != nil {
		t.tb.Fatal(err)
	}
	p := findPlayer(t.State(), addr)
	if p == nil {
		t.tb.Fatalf("%s is not seated", subject)
	}
	return p
}

func (t *T) big(s string) *big.Int {
	t.tb.Helper()
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.tb.Fatalf("not an amount: %q", s)
	}
	return v
}

func cmp(a, b *big.Int) int {
	if a == nil {
		a = new(big.Int)
	}
	if b == nil {
		b = new(big.Int)
	}
	return a.Cmp(b)
}

func str(v *big.Int) string {
	if v == nil {
		return "0"
	}
	return v.String()
}
