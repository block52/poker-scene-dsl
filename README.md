# poker-scene-dsl

Write poker engine tests the way a hand history reads.

```go
sc := scene.NewT(t, table, map[string]string{"Alice": alice, "Bob": bob})

sc.MustJoin("Alice", 1, "1000")
sc.MustJoin("Bob", 2, "1000")
sc.Act("Alice sits in", "Bob sits in")
sc.Act("SB posts small blind", "BB posts big blind", "deal")

sc.Act("SB calls", "BB raises to 3bb")
sc.AssertPot("400")
sc.Rejects("BB checks") // not BB's turn
sc.Act("SB calls")
sc.AssertRound(scene.Flop)

sc.Act("BB bets 200", "SB folds")
sc.AssertStacks(map[string]string{"Alice": "700", "Bob": "1300"})
sc.AssertChips("2000")
```

Each sentence names a **subject** and a **verb**. The DSL works out which player that is and what chips go in, then sends the action to your engine through a small interface. Assertions read the table back in the same vocabulary, so a test never handles an address or a big-integer amount.

## Subjects

- **Positions:** `SB`, `BB`, `BTN` (`button`, `dealer`), `UTG`, `UTG+1`, `MP`, `MP1`, `MP2`, `HJ` (`hijack`), `CO` (`cutoff`). Positions are worked out from the button and blinds for 2 to 9 seated players.
- **Seats:** `seat 4`
- **Labels:** any name you register (`Alice`, `P1`)
- **Addresses:** an address, as-is

## Verbs

| Sentence | Sends |
|---|---|
| `X posts small blind` / `posts sb`, `X posts big blind` / `posts bb` | the blind, at the amount the engine offers |
| `X folds`, `X checks` (`taps`) | |
| `X calls` (`flats`, `limps`) | the call, at the amount the engine offers |
| `X bets N` (`leads for N`) | a bet with N as the **street total** |
| `X raises to N` (`makes it N`, `3-bets to N`) | a raise to a **street total** of N, so the chips already in are subtracted |
| `X raises by N` / `X raises N more` | a raise of N chips going in now |
| `X shoves` (`jams`, `goes all-in`) | whichever of all-in / raise / bet / call the engine offers, at its max |
| `X shows`, `X mucks` | |
| `X sits out`, `X sits in`, `X sits in and waits`, `X leaves` (`stands up`), `X tops up N` | |
| `X joins seat N with B` (`sits down in seat N for B`) | a join; X is a label or an address |
| `deal`, `new hand`, `new hand with deck AS-KD-…`, `run it out` | no subject: sent by the seat on the clock. `run it out` shows every pending reveal until the hand ends. |
| `time runs out` | a timeout for the seat on the clock, ten minutes after the last action |
| `N seconds pass`, `N minutes pass`, `N hours pass` | no subject: the clock moves on with nothing sent, so the next action is that much later |
| `X starts the tournament` | a `start` action: a registered player starts a scheduled tournament |

Each accepted action is stamped a second after the previous one, so a scenario always runs on the same timeline.

Amounts are chips (`300`), or multiples of the blinds (`3bb`, `2sb`).

## Assertions

`AssertRound`, `AssertPot`, `AssertBoard`, `AssertStack`, `AssertStacks`, `AssertChips` (stacks + pot), `AssertStatus`, `AssertToAct`, `AssertLegal`, `AssertNotLegal`, `AssertRange` (a legal action's min and max), and `Rejects(sentence)`, which checks that the engine refuses the action and the table doesn't change.

Outside tests, `scene.New(table, labels).Do(sentence)` returns an error instead of failing, and `scene.Parse(sentence)` parses a sentence without a table.

## Plug in an engine

```go
type Table interface {
	State() scene.State      // round, positions, blinds, pot, board, players with legal actions
	Apply(scene.Action) error // reject with an error, leaving the table unchanged
}
```

Every bet, raise and call carries both `Amount` (the chips going in now) and `Total` (the player's street total afterwards), so the adapter sends whichever its engine expects. `Player.StreetBet` is what the player already has in on this street; "raises to" needs it.

## Adapters

### [poker-engine-ts](https://github.com/Ge-limin/poker-engine-ts)

`adapters/pokerenginets` drives the TypeScript engine through a small Node bridge. To run it:

```sh
cd adapters/pokerenginets && npm ci
go test ./adapters/pokerenginets                          # scenarios that pass
POKER_ENGINE_TS_BUGS=1 go test -v ./adapters/pokerenginets # include the known-bug scenarios
```

poker-engine-ts seats players when a session is created, and posts blinds and deals by itself. So joins work until the first hand, blind, `sits in` and `deal` sentences are no-ops, and leaving, sitting out, topping up and timeouts aren't supported.

The scenarios cover blinds, folds, rejections, the opening minimum raise, showdowns, a three-way all-in with side pots and an uncalled return, a split pot, button and blind movement, and four-handed postflop play with a short all-in. They also found two engine bugs, which are kept as skipped scenarios:

- **A posted blind isn't credited towards a preflop call** ([upstream #1](https://github.com/Ge-limin/poker-engine-ts/issues/1)). The small blind is charged 100 to complete a 100 big blind, and the big blind then faces a bet instead of having the option to check. When the blinds call a raise to 300, the pot comes to 1050 instead of 900.
- **The first preflop raise is sized from 0, not from the big blind,** so after a raise to 300 the minimum re-raise is to 600, not 500. Later raises are sized correctly. ([upstream #2](https://github.com/Ge-limin/poker-engine-ts/issues/2))

## License

MIT
