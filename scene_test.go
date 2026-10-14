package scene

import (
	"errors"
	"math/big"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Step
	}{
		{"SB posts small blind", Step{Subject: "SB", Verb: PostSmallBlind}},
		{"seat 2 posts bb", Step{Subject: "seat 2", Verb: PostBigBlind}},
		{"BTN raises to 3bb", Step{Subject: "BTN", Verb: RaiseTo, Amount: "3bb"}},
		{"Alice makes it 300", Step{Subject: "Alice", Verb: RaiseTo, Amount: "300"}},
		{"Alice raises 300", Step{Subject: "Alice", Verb: RaiseTo, Amount: "300"}},
		{"Bob raises by 200", Step{Subject: "Bob", Verb: RaiseBy, Amount: "200"}},
		{"Bob raises 200 more", Step{Subject: "Bob", Verb: RaiseBy, Amount: "200"}},
		{"CO 3-bets to 900", Step{Subject: "CO", Verb: RaiseTo, Amount: "900"}},
		{"UTG bets 150", Step{Subject: "UTG", Verb: Bet, Amount: "150"}},
		{"UTG leads for 2bb", Step{Subject: "UTG", Verb: Bet, Amount: "2bb"}},
		{"HJ jams", Step{Subject: "HJ", Verb: Shove}},
		{"HJ goes all-in", Step{Subject: "HJ", Verb: Shove}},
		{"BB calls", Step{Subject: "BB", Verb: Call}},
		{"MP limps", Step{Subject: "MP", Verb: Call}},
		{"BB checks", Step{Subject: "BB", Verb: Check}},
		{"Carol folds", Step{Subject: "Carol", Verb: Fold}},
		{"Carol sits in and waits", Step{Subject: "Carol", Verb: SitInAndWait}},
		{"Carol sits in", Step{Subject: "Carol", Verb: SitIn}},
		{"Carol sits out", Step{Subject: "Carol", Verb: SitOut}},
		{"Carol stands up", Step{Subject: "Carol", Verb: Leave}},
		{"Carol tops up 500", Step{Subject: "Carol", Verb: TopUp, Amount: "500"}},
		{"deal", Step{Verb: Deal}},
		{"new hand", Step{Verb: NewHand}},
		{"run it out", Step{Verb: RunOut}},
		{"time runs out", Step{Verb: TimeOut}},
		{"Dave joins seat 4 with 1000", Step{Subject: "Dave", Verb: Join, Seat: 4, Amount: "1000"}},
		{"player05 sits down in seat 3 for 20bb", Step{Subject: "player05", Verb: Join, Seat: 3, Amount: "20bb"}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if got.Subject != c.want.Subject || got.Verb != c.want.Verb || got.Amount != c.want.Amount || got.Seat != c.want.Seat {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
	if got, err := Parse("new hand with deck AS-KD-2C"); err != nil || got.Verb != NewHand || len(got.Deck) != 3 || got.Deck[1] != "KD" {
		t.Errorf("new hand with deck: got %+v, %v", got, err)
	}
	for _, bad := range []string{"", "Alice dances", "Alice bets lots"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", bad)
		}
	}
}

// recorder is a table with a fixed state that records what it is sent.
type recorder struct {
	state State
	sent  []Action
}

func (r *recorder) State() State { return r.state }
func (r *recorder) Apply(a Action) error {
	if a.Action == "reject-me" {
		return errors.New("rejected")
	}
	r.sent = append(r.sent, a)
	return nil
}

func n(v int64) *big.Int { return big.NewInt(v) }

// sixMax is a 6-handed table on the flop: button seat 6, blinds seats 1 and 2.
func sixMax() *recorder {
	st := State{
		Round: Flop, Dealer: 6, SmallBlind: 1, BigBlind: 2, NextToAct: 3,
		SmallBlindAmount: n(50), BigBlindAmount: n(100), Pot: n(400),
	}
	for seat := 1; seat <= 6; seat++ {
		st.Players = append(st.Players, Player{
			Address: "addr" + string(rune('0'+seat)), Seat: seat, Stack: n(1000), StreetBet: n(0), Status: "active",
			Legal: []LegalAction{{Action: ActCheck}, {Action: ActBet, Min: n(100), Max: n(1000)}},
		})
	}
	// Seat 4 already bet 200 on this street and faces a raise.
	st.Players[3].StreetBet = n(200)
	st.Players[3].Legal = []LegalAction{{Action: ActFold}, {Action: ActCall, Min: n(400), Max: n(400)}, {Action: ActRaise, Min: n(800), Max: n(800)}}
	return &recorder{state: st}
}

func TestPositions(t *testing.T) {
	r := sixMax()
	sc := New(r, map[string]string{"Alice": "addr5"})
	for subject, want := range map[string]string{
		"SB": "addr1", "BB": "addr2", "UTG": "addr3", "MP": "addr4", "CO": "addr5", "BTN": "addr6",
		"button": "addr6", "seat 4": "addr4", "Alice": "addr5", "addr3": "addr3",
	} {
		got, err := sc.Resolve(subject)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", subject, got, err, want)
		}
	}
	if _, err := sc.Resolve("HJ"); err == nil {
		t.Error("HJ does not exist 6-handed; want an error")
	}
}

func TestAmounts(t *testing.T) {
	r := sixMax()
	sc := New(r, nil)
	if err := sc.Do("UTG bets 3bb"); err != nil {
		t.Fatal(err)
	}
	if a := r.sent[0]; a.Action != ActBet || a.Amount.Int64() != 300 || a.Total.Int64() != 300 {
		t.Fatalf("bet 3bb: got %s amount=%v total=%v", a.Action, a.Amount, a.Total)
	}
	// "raises to" is the street total: seat 4 has 200 in, so 600 more goes in.
	if err := sc.Do("MP raises to 800"); err != nil {
		t.Fatal(err)
	}
	if a := r.sent[1]; a.Action != ActRaise || a.Amount.Int64() != 600 || a.Total.Int64() != 800 {
		t.Fatalf("raise to 800: got amount=%v total=%v", a.Amount, a.Total)
	}
	if err := sc.Do("MP raises by 600"); err != nil {
		t.Fatal(err)
	}
	if a := r.sent[2]; a.Amount.Int64() != 600 || a.Total.Int64() != 800 {
		t.Fatalf("raise by 600: got amount=%v total=%v", a.Amount, a.Total)
	}
	if err := sc.Do("MP calls"); err != nil {
		t.Fatal(err)
	}
	if a := r.sent[3]; a.Action != ActCall || a.Amount.Int64() != 400 || a.Total.Int64() != 600 {
		t.Fatalf("call: got amount=%v total=%v", a.Amount, a.Total)
	}
	// A shove goes through the raise the engine offers, at its max.
	if err := sc.Do("MP jams"); err != nil {
		t.Fatal(err)
	}
	if a := r.sent[4]; a.Action != ActRaise || a.Amount.Int64() != 800 {
		t.Fatalf("jam: got %s %v", a.Action, a.Amount)
	}
}

func TestDealGoesToTheSeatOnTheClock(t *testing.T) {
	r := sixMax()
	sc := New(r, nil)
	sc.NextDeck = func() []string { return []string{"AS", "KS"} }
	if err := sc.Do("deal"); err != nil {
		t.Fatal(err)
	}
	if err := sc.Do("new hand"); err != nil {
		t.Fatal(err)
	}
	if r.sent[0].Player != "addr3" || r.sent[0].Action != ActDeal {
		t.Fatalf("deal: got %+v", r.sent[0])
	}
	if r.sent[1].Action != ActNewHand || len(r.sent[1].Deck) != 2 {
		t.Fatalf("new hand: got %+v", r.sent[1])
	}
	if r.sent[1].Time <= r.sent[0].Time {
		t.Fatal("timestamps must increase")
	}
}

func TestAssertions(t *testing.T) {
	sc := NewT(t, sixMax(), map[string]string{"Alice": "addr5"})
	sc.AssertRound(Flop)
	sc.AssertPot("400")
	sc.AssertStack("Alice", "1000")
	sc.AssertStacks(map[string]string{"SB": "1000", "BTN": "1000"})
	sc.AssertStatus("BB", "active")
	sc.AssertToAct("UTG")
	sc.AssertLegal("MP", ActCall, ActRaise)
	sc.AssertNotLegal("MP", ActCheck)
	sc.AssertRange("MP", ActCall, "400", "400")
	sc.AssertChips("6400")
}

func TestJoinAndTimeout(t *testing.T) {
	r := sixMax()
	sc := New(r, map[string]string{"Dave": "addrD"})
	if err := sc.Do("Dave joins seat 7 with 10bb"); err != nil {
		t.Fatal(err)
	}
	if err := sc.Do("player09 joins seat 8 with 500"); err != nil {
		t.Fatal(err)
	}
	if err := sc.Do("time runs out"); err != nil {
		t.Fatal(err)
	}
	j1, j2, to := r.sent[0], r.sent[1], r.sent[2]
	if j1.Action != ActJoin || j1.Player != "addrD" || j1.Seat != 7 || j1.Amount.Int64() != 1000 {
		t.Fatalf("join by label: got %+v", j1)
	}
	if j2.Player != "player09" || j2.Seat != 8 {
		t.Fatalf("join by raw address: got %+v", j2)
	}
	if to.Action != ActTimeout || to.Player != "" || to.Time-j2.Time < 10*60*1000 {
		t.Fatalf("timeout: got %+v", to)
	}
}
