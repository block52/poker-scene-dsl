package scene

import (
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Scene applies sentences to a table.
type Scene struct {
	table  Table
	labels map[string]string
	clock  int64
	// NextDeck supplies the deck for a "new hand" sentence. Nil sends none,
	// for engines that shuffle themselves.
	NextDeck func() []string
}

// New wraps a table. labels maps names used in sentences ("Alice", "P1") to
// player addresses.
func New(table Table, labels map[string]string) *Scene {
	if labels == nil {
		labels = map[string]string{}
	}
	return &Scene{table: table, labels: labels, clock: 1_700_000_000_000}
}

// State is the table's current state.
func (s *Scene) State() State { return s.table.State() }

// send applies a to the table, stamping it a second after the last accepted
// action (ten minutes and a second for a timeout). The clock moves only when
// the engine accepts, so a rejected sentence leaves the timeline unchanged.
func (s *Scene) send(a Action) error {
	a.Time = s.clock + 1000
	if a.Action == ActTimeout {
		a.Time += 10 * 60 * 1000
	}
	if err := s.table.Apply(a); err != nil {
		return err
	}
	s.clock = a.Time
	return nil
}

// Do parses one sentence and applies it.
func (s *Scene) Do(sentence string) error {
	step, err := Parse(sentence)
	if err != nil {
		return err
	}
	if err := s.Apply(step); err != nil {
		return fmt.Errorf("scene: %q: %w", sentence, err)
	}
	return nil
}

// Join seats subject at seat with buyIn.
func (s *Scene) Join(subject string, seat int, buyIn *big.Int) error {
	addr, err := s.Resolve(subject)
	if err != nil {
		return err
	}
	return s.send(Action{Player: addr, Action: ActJoin, Amount: buyIn, Seat: seat})
}

// NewHandWith starts the next hand with an explicit deck, performed by subject.
func (s *Scene) NewHandWith(subject string, deck []string) error {
	addr, err := s.Resolve(subject)
	if err != nil {
		return err
	}
	return s.send(Action{Player: addr, Action: ActNewHand, Deck: deck})
}

// Apply runs a parsed step.
func (s *Scene) Apply(step Step) error {
	switch step.Verb {
	case Deal:
		return s.nonPlayer(ActDeal, nil)
	case NewHand:
		deck := step.Deck
		if deck == nil && s.NextDeck != nil {
			deck = s.NextDeck()
		}
		return s.nonPlayer(ActNewHand, deck)
	case RunOut:
		return s.runOut()
	case TimeOut:
		return s.send(Action{Action: ActTimeout})
	case Join:
		buyIn, err := s.amount(s.table.State(), step.Amount)
		if err != nil {
			return err
		}
		addr, err := s.Resolve(step.Subject)
		if err != nil {
			// Not seated yet: a raw address joins as-is.
			addr = strings.TrimSpace(step.Subject)
		}
		return s.send(Action{Player: addr, Action: ActJoin, Amount: buyIn, Seat: step.Seat})
	}

	addr, err := s.Resolve(step.Subject)
	if err != nil {
		return err
	}
	st := s.table.State()
	p := findPlayer(st, addr)
	if p == nil {
		return fmt.Errorf("%s is not seated", step.Subject)
	}
	a := Action{Player: addr}

	switch step.Verb {
	case PostSmallBlind:
		a.Action = ActPostSmallBlind
		a.Amount = legalMin(p, ActPostSmallBlind, st.SmallBlindAmount)
	case PostBigBlind:
		a.Action = ActPostBigBlind
		a.Amount = legalMin(p, ActPostBigBlind, st.BigBlindAmount)
	case Fold:
		a.Action = ActFold
	case Check:
		a.Action = ActCheck
	case Show:
		a.Action = ActShow
	case Muck:
		a.Action = ActMuck
	case SitOut:
		a.Action = ActSitOut
	case SitIn:
		a.Action = ActSitIn
	case SitInAndWait:
		a.Action = ActSitInAndWait
	case Leave:
		a.Action = ActLeave
	case Call:
		a.Action = ActCall
		a.Amount = legalMin(p, ActCall, nil)
		if a.Amount == nil {
			return fmt.Errorf("%s has no call (legal: %s)", step.Subject, legalNames(p))
		}
		a.Total = new(big.Int).Add(streetBet(p), a.Amount)
	case Bet, RaiseTo, RaiseBy:
		amt, err := s.amount(st, step.Amount)
		if err != nil {
			return err
		}
		a.Action = ActRaise
		if step.Verb == Bet {
			a.Action = ActBet
		}
		if step.Verb == RaiseBy {
			a.Amount = amt
			a.Total = new(big.Int).Add(streetBet(p), amt)
		} else {
			a.Total = amt
			a.Amount = new(big.Int).Sub(amt, streetBet(p))
		}
	case TopUp:
		amt, err := s.amount(st, step.Amount)
		if err != nil {
			return err
		}
		a.Action, a.Amount = ActTopUp, amt
	case Shove:
		// An all-in goes through whichever action the engine offers, at its
		// max: an explicit all-in, else a raise facing a bet, a bet on an open
		// street, or a call that already covers the stack.
		for _, name := range []string{ActAllIn, ActRaise, ActBet, ActCall} {
			if la := legal(p, name); la != nil {
				a.Action = name
				a.Amount = la.Max
				if a.Amount == nil {
					a.Amount = p.Stack
				}
				a.Total = new(big.Int).Add(streetBet(p), a.Amount)
				break
			}
		}
		if a.Action == "" {
			return fmt.Errorf("%s cannot shove (legal: %s)", step.Subject, legalNames(p))
		}
	default:
		return fmt.Errorf("unknown verb %q", step.Verb)
	}
	return s.send(a)
}

// nonPlayer sends a subjectless action as the player on the clock, or the
// first seated player with it legal.
func (s *Scene) nonPlayer(action string, deck []string) error {
	st := s.table.State()
	performer := ""
	for _, p := range st.Players {
		if p.Seat == st.NextToAct {
			performer = p.Address
		}
	}
	if performer == "" {
		for i := range st.Players {
			if legal(&st.Players[i], action) != nil {
				performer = st.Players[i].Address
				break
			}
		}
	}
	if performer == "" {
		return fmt.Errorf("nobody can %s", action)
	}
	return s.send(Action{Player: performer, Action: action, Deck: deck})
}

// runOut answers every showdown reveal with a show until the hand ends.
func (s *Scene) runOut() error {
	for range 64 {
		st := s.table.State()
		if st.Round != Showdown {
			return nil
		}
		var p *Player
		for i := range st.Players {
			if st.Players[i].Seat == st.NextToAct {
				p = &st.Players[i]
			}
		}
		if p == nil || legal(p, ActShow) == nil {
			return fmt.Errorf("showdown is waiting on seat %d, which cannot show", st.NextToAct)
		}
		if err := s.send(Action{Player: p.Address, Action: ActShow}); err != nil {
			return err
		}
	}
	return fmt.Errorf("showdown did not finish")
}

var amountRe = regexp.MustCompile(`(?i)^(\d+)(bb|sb)?$`)

func (s *Scene) amount(st State, tok string) (*big.Int, error) {
	m := amountRe.FindStringSubmatch(tok)
	if m == nil {
		return nil, fmt.Errorf("bad amount %q", tok)
	}
	n, _ := new(big.Int).SetString(m[1], 10)
	switch strings.ToLower(m[2]) {
	case "bb":
		if st.BigBlindAmount == nil {
			return nil, fmt.Errorf("%q needs the table's big blind", tok)
		}
		n.Mul(n, st.BigBlindAmount)
	case "sb":
		if st.SmallBlindAmount == nil {
			return nil, fmt.Errorf("%q needs the table's small blind", tok)
		}
		n.Mul(n, st.SmallBlindAmount)
	}
	return n, nil
}

// Positions from the big blind forward, ending at the button, by number of
// seated players. SB and BB come from the table.
var positionOrder = map[int][]string{
	2: {},
	3: {"BTN"},
	4: {"UTG", "BTN"},
	5: {"UTG", "CO", "BTN"},
	6: {"UTG", "MP", "CO", "BTN"},
	7: {"UTG", "MP", "HJ", "CO", "BTN"},
	8: {"UTG", "UTG+1", "MP", "HJ", "CO", "BTN"},
	9: {"UTG", "UTG+1", "MP1", "MP2", "HJ", "CO", "BTN"},
}

var positionAliases = map[string]string{
	"sb": "SB", "small blind": "SB", "bb": "BB", "big blind": "BB",
	"btn": "BTN", "button": "BTN", "dealer": "BTN",
	"utg": "UTG", "utg+1": "UTG+1", "utg1": "UTG+1",
	"mp": "MP", "mp1": "MP1", "mp2": "MP2",
	"hj": "HJ", "hijack": "HJ", "co": "CO", "cutoff": "CO",
}

var seatRe = regexp.MustCompile(`^seat\s+(\d+)$`)

// Resolve maps a subject to a player address: a position (SB, BB, BTN, UTG,
// MP, HJ, CO, …), "seat N", a label, or an address as-is.
func (s *Scene) Resolve(subject string) (string, error) {
	norm := strings.ToLower(strings.TrimSpace(subject))
	st := s.table.State()
	if pos, ok := positionAliases[norm]; ok {
		seat, err := positionSeat(st, pos)
		if err != nil {
			return "", err
		}
		return addressAt(st, seat, pos)
	}
	if m := seatRe.FindStringSubmatch(norm); m != nil {
		seat, _ := strconv.Atoi(m[1])
		return addressAt(st, seat, subject)
	}
	for label, addr := range s.labels {
		if strings.ToLower(label) == norm {
			return addr, nil
		}
	}
	for _, p := range st.Players {
		if p.Address == strings.TrimSpace(subject) {
			return p.Address, nil
		}
	}
	labels := make([]string, 0, len(s.labels))
	for k := range s.labels {
		labels = append(labels, k)
	}
	sort.Strings(labels)
	return "", fmt.Errorf("unknown subject %q: use a position (SB, BB, BTN, UTG, MP, HJ, CO), \"seat N\", an address, or a label (%s)",
		subject, strings.Join(labels, ", "))
}

func positionSeat(st State, pos string) (int, error) {
	switch pos {
	case "SB":
		return st.SmallBlind, nil
	case "BB":
		return st.BigBlind, nil
	case "BTN":
		if st.Dealer == 0 {
			return 0, fmt.Errorf("no button yet")
		}
		return st.Dealer, nil
	}
	ring := make([]int, 0, len(st.Players))
	for _, p := range st.Players {
		ring = append(ring, p.Seat)
	}
	sort.Ints(ring)
	layout, ok := positionOrder[len(ring)]
	if !ok {
		return 0, fmt.Errorf("no positions for %d players", len(ring))
	}
	bb := -1
	for i, seat := range ring {
		if seat == st.BigBlind {
			bb = i
		}
	}
	if bb < 0 {
		return 0, fmt.Errorf("big blind seat %d is not seated", st.BigBlind)
	}
	for i, p := range layout {
		if p == pos {
			return ring[(bb+1+i)%len(ring)], nil
		}
	}
	return 0, fmt.Errorf("no %s at a %d-handed table (positions: SB, BB, %s)", pos, len(ring), strings.Join(layout, ", "))
}

func addressAt(st State, seat int, label string) (string, error) {
	for _, p := range st.Players {
		if p.Seat == seat {
			return p.Address, nil
		}
	}
	return "", fmt.Errorf("%s is seat %d, which is empty", label, seat)
}

func findPlayer(st State, addr string) *Player {
	for i := range st.Players {
		if st.Players[i].Address == addr {
			return &st.Players[i]
		}
	}
	return nil
}

func legal(p *Player, action string) *LegalAction {
	for i := range p.Legal {
		if p.Legal[i].Action == action {
			return &p.Legal[i]
		}
	}
	return nil
}

func legalMin(p *Player, action string, fallback *big.Int) *big.Int {
	if la := legal(p, action); la != nil && la.Min != nil {
		return la.Min
	}
	return fallback
}

func legalNames(p *Player) string {
	names := make([]string, 0, len(p.Legal))
	for _, la := range p.Legal {
		names = append(names, la.Action)
	}
	return "[" + strings.Join(names, " ") + "]"
}

func streetBet(p *Player) *big.Int {
	if p.StreetBet == nil {
		return new(big.Int)
	}
	return p.StreetBet
}
