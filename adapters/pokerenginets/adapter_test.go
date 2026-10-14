package pokerenginets

import (
	"os"
	"slices"
	"testing"

	scene "github.com/block52/poker-scene-dsl"
)

// Scenarios against poker-engine-ts. Each asserts correct no-limit hold'em.
// Scenarios that hit a known engine bug are skipped unless
// POKER_ENGINE_TS_BUGS=1, so `go test` stays green and the bugs stay visible.

var cast = map[string]string{"Alice": "alice", "Bob": "bob", "Carol": "carol", "Dave": "dave"}

func knownEngineBug(t *testing.T, what string) {
	t.Helper()
	if os.Getenv("POKER_ENGINE_TS_BUGS") == "" {
		t.Skipf("known poker-engine-ts bug: %s (set POKER_ENGINE_TS_BUGS=1 to run)", what)
	}
}

// deck lays out a deck the way poker-engine-ts deals it: two cards to each
// player in turn starting left of the button, then burn-flop-burn-turn-burn-river.
func deck(holes [][2]string, board [5]string) []string {
	var d []string
	for _, h := range holes {
		d = append(d, h[0], h[1])
	}
	d = append(d, "2C", board[0], board[1], board[2], "3C", board[3], "4C", board[4])
	for _, suit := range "CDHS" {
		for _, rank := range "23456789TJQKA" {
			if c := string(rank) + string(suit); !slices.Contains(d, c) {
				d = append(d, c)
			}
		}
	}
	return d
}

// A dry board: no pair, flush or straight, so the best pocket pair wins.
var dry = [5]string{"7D", "9H", "3S", "5D", "JC"}

// table seats the players at seats 1.., Alice on the button for hand 1.
func table(t *testing.T, cards []string, players []string, stacks []string) *scene.T {
	t.Helper()
	if ok, why := Available(""); !ok {
		t.Skip(why)
	}
	if cards == nil {
		cards = deck(nil, dry) // no showdown expected: any fixed deck
	}
	tbl, err := New(Options{MaxSeats: len(players), SmallBlind: 50, BigBlind: 100, Deck: cards})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tbl.Close() })
	sc := scene.NewT(t, tbl, cast)
	for i, p := range players {
		sc.Act(p + " joins seat " + string(rune('1'+i)) + " with " + stacks[i])
	}
	sc.Act("deal")
	return sc
}

func threeHanded(t *testing.T, cards []string, stacks ...string) *scene.T {
	if stacks == nil {
		stacks = []string{"1000", "1000", "1000"}
	}
	return table(t, cards, []string{"Alice", "Bob", "Carol"}, stacks)
}

func TestBlindsAndFirstToAct(t *testing.T) {
	sc := threeHanded(t, nil)
	sc.AssertRound(scene.Preflop)
	sc.AssertPot("150")
	sc.AssertStacks(map[string]string{"SB": "950", "BB": "900", "BTN": "1000"})
	sc.AssertToAct("BTN") // three-handed, the button is first preflop
	sc.AssertStatus("Bob", "active")
}

func TestFoldsToTheBigBlind(t *testing.T) {
	sc := threeHanded(t, nil)
	sc.Act("BTN folds", "SB folds")
	sc.AssertRound(scene.End)
	sc.AssertStacks(map[string]string{"Alice": "1000", "Bob": "950", "Carol": "1050"})
	sc.AssertChips("3000")
}

func TestRaiseTakesTheBlinds(t *testing.T) {
	sc := threeHanded(t, nil)
	sc.Act("BTN raises to 300", "SB folds", "BB folds")
	sc.AssertRound(scene.End)
	sc.AssertStacks(map[string]string{"Alice": "1150", "Bob": "950", "Carol": "900"})
	sc.AssertChips("3000")
}

func TestOutOfTurnAndIllegalActionsAreRejected(t *testing.T) {
	sc := threeHanded(t, nil)
	sc.Rejects("BB folds")   // not BB's turn
	sc.Rejects("BTN checks") // facing the big blind
	sc.Act("BTN raises to 300")
	sc.Rejects("SB checks") // facing a raise
	sc.AssertPot("450")
}

func TestMinimumReraise(t *testing.T) {
	knownEngineBug(t, "the no-limit minimum raise is twice the bet, not the bet plus the last raise")
	sc := threeHanded(t, nil)
	sc.Act("BTN raises to 300")
	// The raise was 200 more, so the smallest re-raise is to 500. Bob has 50
	// in, so 450 goes in; the most is his whole stack.
	sc.AssertRange("SB", "raise", "450", "950")
}

func TestOpeningMinimumRaise(t *testing.T) {
	sc := threeHanded(t, nil)
	// Facing only the big blind, the minimum raise is to 200: 2 × 100 and
	// 100 + 100 agree here.
	sc.AssertRange("BTN", "raise", "200", "1000")
	sc.Rejects("BTN raises to 150")
	sc.Act("BTN raises to 200")
	sc.AssertPot("350")
}

func TestShowdownBestHandWins(t *testing.T) {
	// Deal starts left of the button: Bob, Carol, then Alice.
	sc := threeHanded(t, deck([][2]string{{"KS", "KH"}, {"QS", "QH"}, {"AS", "AH"}}, dry))
	sc.Act("BTN jams", "SB jams", "BB jams")
	sc.AssertRound(scene.End)
	sc.AssertBoard("7D", "9H", "3S", "5D", "JC")
	sc.AssertStacks(map[string]string{"Alice": "3000", "Bob": "0", "Carol": "0"})
	sc.AssertChips("3000")
}

func TestThreeWayAllInSidePots(t *testing.T) {
	// Alice 1000 (QQ), Bob 600 (AA), Carol 800 (KK), all in. Bob's AA takes
	// the main pot (3 × 600); Carol's KK beats Alice's QQ for the side pot
	// (2 × 200); Alice's last 200 is unmatched and comes back.
	sc := threeHanded(t, deck([][2]string{{"AS", "AH"}, {"KS", "KH"}, {"QS", "QH"}}, dry), "1000", "600", "800")
	sc.Act("BTN jams", "SB jams", "BB jams")
	sc.AssertRound(scene.End)
	sc.AssertStacks(map[string]string{"Bob": "1800", "Carol": "400", "Alice": "200"})
	sc.AssertChips("2400")
}

func TestSplitPot(t *testing.T) {
	// Board plays: a straight on board, both players play it.
	board := [5]string{"5D", "6H", "7S", "8D", "9C"}
	sc := table(t, deck([][2]string{{"2S", "2H"}, {"3S", "3H"}}, board), []string{"Alice", "Bob"}, []string{"1000", "1000"})
	sc.Act("BTN jams", "BB jams")
	sc.AssertRound(scene.End)
	sc.AssertStacks(map[string]string{"Alice": "1000", "Bob": "1000"})
}

func TestButtonAndBlindsMoveEachHand(t *testing.T) {
	sc := threeHanded(t, nil)
	sc.Act("BTN folds", "SB folds", "new hand")
	sc.AssertRound(scene.Preflop)
	// Carol won hand 1's blinds; hand 2 has Bob on the button, Carol in the
	// small blind and Alice in the big blind.
	sc.AssertStacks(map[string]string{"BTN": "950", "SB": "1000", "BB": "900"})
	sc.AssertStatus("BTN", "active")
	if st := sc.State(); st.Dealer != 2 || st.SmallBlind != 3 || st.BigBlind != 1 {
		t.Fatalf("hand 2 positions: button %d, SB %d, BB %d; want 2, 3, 1", st.Dealer, st.SmallBlind, st.BigBlind)
	}
	sc.AssertChips("3000")
}

func TestPostflopBettingFourHanded(t *testing.T) {
	// Seats: Alice (BTN), Bob (SB), Carol (BB), Dave (UTG). Dave raises and
	// Alice calls; the blinds fold. Dave has AA, Alice KK.
	cards := deck([][2]string{{"8S", "8H"}, {"6S", "6H"}, {"AS", "AH"}, {"KS", "KH"}}, dry)
	sc := table(t, cards, []string{"Alice", "Bob", "Carol", "Dave"}, []string{"1000", "1000", "1000", "1000"})
	sc.AssertToAct("UTG")
	sc.Act("UTG raises to 300", "BTN calls", "SB folds", "BB folds")
	sc.AssertRound(scene.Flop)
	sc.AssertPot("750")
	sc.AssertToAct("Dave") // first live player left of the button
	sc.Act("Dave bets 200", "Alice raises to 600")
	sc.Act("Dave jams", "Alice calls")
	sc.AssertRound(scene.End)
	sc.AssertStacks(map[string]string{"Dave": "2150", "Alice": "0", "Bob": "950", "Carol": "900"})
	sc.AssertChips("4000")
}

// Known bug: a posted blind is not credited when its player calls preflop,
// so the call is charged in full on top of the blind.

func TestSmallBlindCompletesAndBigBlindChecks(t *testing.T) {
	knownEngineBug(t, "posted blinds are not credited towards a preflop call")
	sc := threeHanded(t, nil)
	sc.Act("BTN calls")
	sc.AssertRange("SB", "call", "50", "50") // 50 posted, 50 more completes
	sc.Act("SB calls")
	sc.AssertStack("SB", "900")
	sc.AssertLegal("BB", "check") // nobody raised: the big blind has the option
	sc.Act("BB checks")
	sc.AssertRound(scene.Flop)
	sc.AssertPot("300")
}

func TestBlindsCallARaise(t *testing.T) {
	knownEngineBug(t, "posted blinds are not credited towards a preflop call")
	sc := threeHanded(t, nil)
	sc.Act("BTN raises to 300", "SB calls", "BB calls")
	sc.AssertRound(scene.Flop)
	sc.AssertPot("900")
	sc.AssertStacks(map[string]string{"Alice": "700", "Bob": "700", "Carol": "700"})
}
