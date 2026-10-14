// Package scene is a plain-English DSL for poker engine tests.
//
// A test reads like a hand history:
//
//	sc := scene.NewT(t, table, map[string]string{"Alice": addrA, "Bob": addrB})
//	sc.Act(
//		"SB posts small blind",
//		"BB posts big blind",
//		"deal",
//		"BTN raises to 3bb",
//		"SB folds",
//		"BB calls",
//	)
//	sc.AssertPot("650")
//	sc.AssertLegal("BB", "check", "bet")
//
// Each sentence resolves a subject (a position like UTG or BTN, "seat N", a
// label, or a raw address) and a verb with an optional amount, then applies
// the action to any engine that implements [Table]. Assertions read the table
// back in the same vocabulary.
package scene

import "math/big"

// Round is the betting round. Ante covers the time before cards are dealt;
// End is the gap between hands.
type Round string

const (
	Ante     Round = "ante"
	Preflop  Round = "preflop"
	Flop     Round = "flop"
	Turn     Round = "turn"
	River    Round = "river"
	Showdown Round = "showdown"
	End      Round = "end"
)

// Action names the DSL sends. An engine adapter maps them to its own.
const (
	ActPostSmallBlind = "post-small-blind"
	ActPostBigBlind   = "post-big-blind"
	ActFold           = "fold"
	ActCheck          = "check"
	ActCall           = "call"
	ActBet            = "bet"
	ActRaise          = "raise"
	ActAllIn          = "all-in"
	ActShow           = "show"
	ActMuck           = "muck"
	ActDeal           = "deal"
	ActNewHand        = "new-hand"
	ActJoin           = "join"
	ActLeave          = "leave"
	ActSitIn          = "sit-in"
	ActSitInAndWait   = "sit-in-and-wait"
	ActSitOut         = "sit-out"
	ActTopUp          = "top-up"
	ActTimeout        = "timeout"
	ActStart          = "start"
)

// LegalAction is one action a player may take now, with its amount range when
// it carries one.
type LegalAction struct {
	Action string
	Min    *big.Int
	Max    *big.Int
}

// Player is one seated player.
type Player struct {
	Address string
	Seat    int
	Stack   *big.Int
	// StreetBet is what the player has put in on the current street. "raises
	// to N" uses it to work out the chips still to go in.
	StreetBet *big.Int
	// Status is the engine's own status string ("active", "folded", …); the
	// DSL only compares it.
	Status string
	Legal  []LegalAction
}

// State is the table as the engine reports it. Seats are 1-based; 0 is none.
type State struct {
	Round      Round
	NextToAct  int
	Dealer     int
	SmallBlind int
	BigBlind   int
	// Blind sizes, for amounts written as "3bb" or "2sb".
	SmallBlindAmount *big.Int
	BigBlindAmount   *big.Int
	// Pot is every chip in the middle, all streets and side pots.
	Pot     *big.Int
	Board   []string
	Players []Player
}

// Action is one engine action. Amount is the chips going in with this action;
// for a bet, raise or call, Total is the player's street total afterwards. An
// adapter sends whichever its engine expects. A timeout has no Player: it acts
// for whoever is on the clock, ten minutes after the previous action.
type Action struct {
	Player string
	Action string
	Amount *big.Int
	Total  *big.Int
	// Seat is the seat to take (join only).
	Seat int
	// Deck is the next hand's deck, top first (new-hand only).
	Deck []string
	// Time is a monotonically increasing timestamp in Unix milliseconds.
	Time int64
}

// Table is the engine under test.
type Table interface {
	State() State
	Apply(Action) error
}
