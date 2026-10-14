package scene

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Verb is what a sentence asks for.
type Verb string

const (
	PostSmallBlind Verb = "post small blind"
	PostBigBlind   Verb = "post big blind"
	Fold           Verb = "fold"
	Check          Verb = "check"
	Call           Verb = "call"
	Bet            Verb = "bet"      // Amount is the street total
	RaiseTo        Verb = "raise to" // Amount is the street total
	RaiseBy        Verb = "raise by" // Amount is the chips going in now
	Shove          Verb = "shove"
	Show           Verb = "show"
	Muck           Verb = "muck"
	SitOut         Verb = "sit out"
	SitIn          Verb = "sit in"
	SitInAndWait   Verb = "sit in and wait"
	Leave          Verb = "leave"
	TopUp          Verb = "top up"
	Deal           Verb = "deal"     // no subject
	NewHand        Verb = "new hand" // no subject; optional "with deck AS-KD-…"
	RunOut         Verb = "run out"  // no subject: answer every showdown reveal until the hand ends
	TimeOut        Verb = "time out" // no subject: the clock runs out on the seat to act
	Join           Verb = "join"     // Seat and Amount (the buy-in)
	Start          Verb = "start"    // a registered player starts a scheduled tournament
	Wait           Verb = "wait"     // no subject: time passes with no action; Wait holds the duration
)

// Step is a parsed sentence. Amount is the raw token ("300", "3bb", "2sb"),
// empty when the verb takes none.
type Step struct {
	Subject string
	Verb    Verb
	Amount  string
	// Seat is the seat to take (join only).
	Seat int
	// Deck is the next hand's deck, top first (new hand only; nil = none given).
	Deck []string
	// WaitMs is how long passes, in milliseconds (wait only).
	WaitMs int64
}

// Verbs, longest phrase first so "sits in and waits" wins over "sits in".
var verbs = []struct {
	verb Verb
	re   *regexp.Regexp
	// amountGroups are the submatch indexes that may hold the amount.
	amountGroups []int
}{
	{PostSmallBlind, regexp.MustCompile(`(?i)^(.+?)\s+(?:posts?\s+(?:the\s+)?small\s+blind|posts?\s+sb)$`), nil},
	{PostBigBlind, regexp.MustCompile(`(?i)^(.+?)\s+(?:posts?\s+(?:the\s+)?big\s+blind|posts?\s+bb)$`), nil},
	{RaiseBy, regexp.MustCompile(`(?i)^(.+?)\s+raises?\s+(?:by\s+(\S+)|(\S+)\s+more)$`), []int{2, 3}},
	{RaiseTo, regexp.MustCompile(`(?i)^(.+?)\s+(?:raises?\s+to|raises?|makes?\s+it|3-?bets?\s+to|4-?bets?\s+to)\s+(\S+)$`), []int{2}},
	{Bet, regexp.MustCompile(`(?i)^(.+?)\s+(?:bets?|leads?(?:\s+for)?)\s+(\S+)$`), []int{2}},
	{Shove, regexp.MustCompile(`(?i)^(.+?)\s+(?:shoves?|jams?|goes?\s+all[\s-]?in|all[\s-]?in)$`), nil},
	{Call, regexp.MustCompile(`(?i)^(.+?)\s+(?:calls?|flats?|limps?)(?:\s+big\s+blind)?$`), nil},
	{Check, regexp.MustCompile(`(?i)^(.+?)\s+(?:checks?|taps?)$`), nil},
	{Fold, regexp.MustCompile(`(?i)^(.+?)\s+folds?$`), nil},
	{Show, regexp.MustCompile(`(?i)^(.+?)\s+shows?$`), nil},
	{Muck, regexp.MustCompile(`(?i)^(.+?)\s+mucks?$`), nil},
	{SitOut, regexp.MustCompile(`(?i)^(.+?)\s+sits?\s+out$`), nil},
	{SitInAndWait, regexp.MustCompile(`(?i)^(.+?)\s+sits?\s+in\s+and\s+waits?(?:\s+for\s+the\s+big\s+blind)?$`), nil},
	{SitIn, regexp.MustCompile(`(?i)^(.+?)\s+sits?\s+in$`), nil},
	{Leave, regexp.MustCompile(`(?i)^(.+?)\s+(?:leaves?|stands?\s+up)$`), nil},
	{TopUp, regexp.MustCompile(`(?i)^(.+?)\s+tops?\s+up(?:\s+by)?\s+(\S+)$`), []int{2}},
	{Start, regexp.MustCompile(`(?i)^(.+?)\s+starts?\s+the\s+(?:tournament|game|sit[\s-]?and[\s-]?go)$`), nil},
}

var (
	amountToken = regexp.MustCompile(`(?i)^\d+(?:bb|sb)?$`)
	joinRe      = regexp.MustCompile(`(?i)^(.+?)\s+(?:joins|sits\s+down)(?:\s+(?:in|at))?\s+seat\s+(\d+)\s+(?:with|for)\s+(\S+)$`)
	newHandRe   = regexp.MustCompile(`(?i)^(?:new|next)\s+hand\s+with\s+deck\s+(\S+)$`)
	waitRe      = regexp.MustCompile(`(?i)^(\d+)\s+(second|minute|hour)s?\s+pass(?:es)?$`)
)

// Parse turns one sentence into a [Step]. It does not look at a table.
func Parse(sentence string) (Step, error) {
	s := strings.TrimSpace(sentence)
	if s == "" {
		return Step{}, fmt.Errorf("scene: empty sentence")
	}
	switch strings.ToLower(s) {
	case "deal", "deal cards":
		return Step{Verb: Deal}, nil
	case "new hand", "next hand":
		return Step{Verb: NewHand}, nil
	case "run it out", "run out", "showdown":
		return Step{Verb: RunOut}, nil
	case "time runs out", "timeout", "the clock runs out":
		return Step{Verb: TimeOut}, nil
	}
	if m := waitRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		unit := map[string]int64{"second": 1000, "minute": 60_000, "hour": 3_600_000}[strings.ToLower(m[2])]
		return Step{Verb: Wait, WaitMs: n * unit}, nil
	}
	if m := newHandRe.FindStringSubmatch(s); m != nil {
		return Step{Verb: NewHand, Deck: strings.Split(m[1], "-")}, nil
	}
	if m := joinRe.FindStringSubmatch(s); m != nil {
		seat, _ := strconv.Atoi(m[2])
		if !amountToken.MatchString(m[3]) {
			return Step{}, fmt.Errorf("scene: %q: buy-in %q is not N, Nbb or Nsb", sentence, m[3])
		}
		return Step{Subject: strings.TrimSpace(m[1]), Verb: Join, Seat: seat, Amount: m[3]}, nil
	}
	for _, v := range verbs {
		m := v.re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		step := Step{Subject: strings.TrimSpace(m[1]), Verb: v.verb}
		for _, g := range v.amountGroups {
			if m[g] != "" {
				step.Amount = m[g]
			}
		}
		if v.amountGroups != nil && !amountToken.MatchString(step.Amount) {
			return Step{}, fmt.Errorf("scene: %q: amount %q is not N, Nbb or Nsb", sentence, step.Amount)
		}
		return step, nil
	}
	return Step{}, fmt.Errorf("scene: could not parse %q. Verbs: posts small/big blind, folds, checks, calls, "+
		"bets N, raises to N, raises by N, shoves, shows, mucks, sits out, sits in, sits in and waits, "+
		"leaves, tops up N, joins seat N with B, starts the tournament; and with no subject: deal, new hand [with deck …], "+
		"run it out, time runs out, N minutes pass", sentence)
}
