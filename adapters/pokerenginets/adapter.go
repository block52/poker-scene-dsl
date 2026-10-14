// Package pokerenginets adapts poker-engine-ts (https://github.com/Ge-limin/poker-engine-ts)
// to the scene DSL. The engine runs in Node behind a small JSON-lines bridge
// (bridge.mjs); this package starts it and maps between the two vocabularies.
//
// Setup: `npm ci` in this directory (installs poker-engine-ts), and `node` on
// PATH.
//
// poker-engine-ts seats every player when a session is created and posts the
// blinds and deals by itself, so:
//
//   - "X joins seat N with B" works until the first hand starts; the first
//     action that needs a hand starts it.
//   - "posts small blind", "posts big blind", "sits in" and "deal" are
//     accepted as no-ops (the engine has already done them).
//   - Leaving, sitting out, topping up and timeouts are not supported and
//     return an error.
package pokerenginets

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	scene "github.com/block52/poker-scene-dsl"
)

// Options configure a table.
type Options struct {
	MaxSeats   int
	SmallBlind int64
	BigBlind   int64
	// Deck for the first hand, top first ("AS", "KD", …). Nil: the engine's
	// own shuffle.
	Deck []string
	// Dir holds bridge.mjs and node_modules. Empty: this package's directory.
	Dir string
}

// Table is one poker-engine-ts session.
type Table struct {
	opts    Options
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *bufio.Scanner
	mu      sync.Mutex
	joined  []joinedSeat
	started bool
	view    *bridgeView
}

type joinedSeat struct {
	ID    string `json:"id"`
	Seat  int    `json:"seat"`
	Stack int64  `json:"stack"`
}

type bridgeView struct {
	HandNumber int    `json:"handNumber"`
	Stage      string `json:"stage"`
	Actor      string `json:"actor"`
	Button     int    `json:"button"`
	SmallBlind string `json:"smallBlind"`
	BigBlind   string `json:"bigBlind"`
	Pot        int64  `json:"pot"`
	Board      []string
	HoleCards  map[string][]string `json:"holeCards"`
	Seats      []struct {
		ID        string `json:"id"`
		Seat      int    `json:"seat"`
		Stack     int64  `json:"stack"`
		StreetBet int64  `json:"streetBet"`
		Status    string `json:"status"`
	} `json:"seats"`
	Legal []struct {
		Type   string `json:"type"`
		Amount *int64 `json:"amount"`
		Min    *int64 `json:"min"`
		Max    *int64 `json:"max"`
	} `json:"legal"`
}

// Dir is this package's directory, where bridge.mjs and node_modules live.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

// Available reports whether the bridge can run: node on PATH and the engine
// installed. Tests skip with the returned reason when it cannot.
func Available(dir string) (bool, string) {
	if dir == "" {
		dir = Dir()
	}
	if _, err := exec.LookPath("node"); err != nil {
		return false, "node is not on PATH"
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "poker-engine-ts")); err != nil {
		return false, "poker-engine-ts is not installed: run `npm ci` in " + dir
	}
	return true, ""
}

// New starts a bridge process. Close it when done.
func New(opts Options) (*Table, error) {
	if opts.Dir == "" {
		opts.Dir = Dir()
	}
	cmd := exec.Command("node", "bridge.mjs")
	cmd.Dir = opts.Dir
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out := bufio.NewScanner(stdout)
	out.Buffer(make([]byte, 1<<20), 1<<24)
	return &Table{opts: opts, cmd: cmd, in: in, out: out}, nil
}

// Close stops the bridge.
func (t *Table) Close() error {
	t.in.Close()
	return t.cmd.Wait()
}

func (t *Table) call(req map[string]any) error {
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := t.in.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("bridge: %w", err)
	}
	if !t.out.Scan() {
		return fmt.Errorf("bridge exited: %v", t.out.Err())
	}
	var resp struct {
		Error string      `json:"error"`
		State *bridgeView `json:"state"`
	}
	if err := json.Unmarshal(t.out.Bytes(), &resp); err != nil {
		return fmt.Errorf("bridge: bad response %q: %w", t.out.Text(), err)
	}
	t.view = resp.State
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

// start creates the session from the players joined so far.
func (t *Table) start() error {
	if t.started {
		return nil
	}
	if len(t.joined) < 2 {
		return fmt.Errorf("poker-engine-ts needs at least two players before the first hand (have %d)", len(t.joined))
	}
	if err := t.call(map[string]any{
		"op": "create", "seats": t.joined, "maxSeats": t.opts.MaxSeats,
		"smallBlind": t.opts.SmallBlind, "bigBlind": t.opts.BigBlind, "deck": engineCards(t.opts.Deck),
	}); err != nil {
		return err
	}
	t.started = true
	return nil
}

// Apply implements scene.Table.
func (t *Table) Apply(a scene.Action) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch a.Action {
	case scene.ActJoin:
		if t.started {
			return fmt.Errorf("poker-engine-ts seats players only before the first hand")
		}
		for _, j := range t.joined {
			if j.Seat == a.Seat-1 || j.ID == a.Player {
				return fmt.Errorf("seat %d is taken", a.Seat)
			}
		}
		t.joined = append(t.joined, joinedSeat{ID: a.Player, Seat: a.Seat - 1, Stack: a.Amount.Int64()})
		return nil
	case scene.ActSitIn:
		if t.started {
			return fmt.Errorf("poker-engine-ts has no sit-in once play has started")
		}
		return nil
	case scene.ActPostSmallBlind, scene.ActPostBigBlind, scene.ActDeal:
		// The engine posts blinds and deals by itself.
		return t.start()
	case scene.ActNewHand:
		if err := t.start(); err != nil {
			return err
		}
		return t.call(map[string]any{"op": "newHand", "deck": engineCards(a.Deck)})
	case scene.ActFold, scene.ActCheck, scene.ActCall, scene.ActBet, scene.ActRaise, scene.ActAllIn:
		if err := t.start(); err != nil {
			return err
		}
		req := map[string]any{"op": "act", "actor": a.Player, "type": a.Action}
		if a.Amount != nil {
			req["amount"] = a.Amount.Int64()
		}
		if a.Total != nil {
			req["to"] = a.Total.Int64()
		}
		return t.call(req)
	default:
		return fmt.Errorf("poker-engine-ts does not support %s", a.Action)
	}
}

// State implements scene.State.
func (t *Table) State() scene.State {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := scene.State{
		SmallBlindAmount: big.NewInt(t.opts.SmallBlind),
		BigBlindAmount:   big.NewInt(t.opts.BigBlind),
		Pot:              new(big.Int),
	}
	if t.view == nil {
		// Not started: show who has joined.
		st.Round = scene.Ante
		for _, j := range t.joined {
			st.Players = append(st.Players, scene.Player{
				Address: j.ID, Seat: j.Seat + 1, Stack: big.NewInt(j.Stack), StreetBet: new(big.Int), Status: "seated",
				// Anyone may start the first hand; the engine then posts the blinds.
				Legal: []scene.LegalAction{{Action: scene.ActDeal}, {Action: scene.ActSitIn}},
			})
		}
		return st
	}
	v := t.view
	st.Round = scene.Round(v.Stage)
	if v.Actor == "" && (v.Stage == "showdown" || v.Stage == "complete" || v.Stage == "settled") {
		st.Round = scene.End
	}
	st.Pot = big.NewInt(v.Pot)
	st.Board = sceneCards(v.Board)
	st.Dealer = v.Button + 1
	for _, s := range v.Seats {
		p := scene.Player{Address: s.ID, Seat: s.Seat + 1, Stack: big.NewInt(s.Stack), StreetBet: big.NewInt(s.StreetBet), Status: s.Status}
		if s.ID == v.Actor {
			st.NextToAct = p.Seat
			p.Legal = t.legal(s.StreetBet)
		}
		if st.Round == scene.End {
			// Between hands anyone may start the next one.
			p.Legal = []scene.LegalAction{{Action: scene.ActNewHand}}
		}
		if s.ID == v.SmallBlind {
			st.SmallBlind = p.Seat
		}
		if s.ID == v.BigBlind {
			st.BigBlind = p.Seat
		}
		st.Players = append(st.Players, p)
	}
	return st
}

// legal maps the actor's options to the DSL's: amounts are the chips going in
// now, so the engine's street-total bet / raise sizes are reduced by what the
// player already has in.
func (t *Table) legal(streetBet int64) []scene.LegalAction {
	var out []scene.LegalAction
	for _, o := range t.view.Legal {
		la := scene.LegalAction{Action: o.Type}
		switch {
		case o.Min != nil && o.Max != nil:
			la.Min, la.Max = big.NewInt(*o.Min-streetBet), big.NewInt(*o.Max-streetBet)
		case o.Amount != nil:
			la.Min, la.Max = big.NewInt(*o.Amount), big.NewInt(*o.Amount)
		}
		out = append(out, la)
	}
	return out
}

// The engine writes suits in lower case ("As"); the DSL in upper ("AS").
func engineCards(cards []string) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		if len(c) == 2 {
			c = c[:1] + strings.ToLower(c[1:])
		}
		out[i] = c
	}
	return out
}

func sceneCards(cards []string) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = strings.ToUpper(c)
	}
	return out
}
