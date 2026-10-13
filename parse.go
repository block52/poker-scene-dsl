package scene

import (
	"fmt"
	"regexp"
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
	NewHand        Verb = "new hand" // no subject
	RunOut         Verb = "run out"  // no subject: answer every showdown reveal until the hand ends
)

// Step is a parsed sentence. Amount is the raw token ("300", "3bb", "2sb"),
// empty when the verb takes none.
type Step struct {
	Subject string
	Verb    Verb
	Amount  string
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
}

var amountToken = regexp.MustCompile(`(?i)^\d+(?:bb|sb)?$`)

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
		"leaves, tops up N; and with no subject: deal, new hand, run it out", sentence)
}
