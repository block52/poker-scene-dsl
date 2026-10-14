// JSON-lines bridge between the Go scene DSL and poker-engine-ts.
//
// Reads one request per line on stdin, writes one response per line on
// stdout. Every response carries the table state after the request, or an
// error. Requests:
//
//   {"op":"create","seats":[{"id","seat","stack"}],"smallBlind","bigBlind","deck":[…]}
//   {"op":"act","actor","type","amount","to"}   fold | check | call | bet | raise | all-in
//   {"op":"newHand","deck":[…]}
//   {"op":"state"}
//
// Hands are dealt through generateRandomState / advanceRandomState with an
// explicit deck: the engine's documented way to start a hand with hole cards
// (SessionManager.create alone deals none).
import { createInterface } from 'node:readline';
import {
  SessionManager,
  advanceRandomState,
  deriveRoundComputation,
  generateRandomState,
  selectTableView,
  toSnapshotEnvelope,
} from 'poker-engine-ts';

const NOW = 1_700_000_000_000;
const now = () => NOW;
let manager = null;
let config = null;
let intents = 0;

function makeConfig(maxSeats, smallBlind, bigBlind) {
  return {
    tableVariant: 'texas-holdem',
    bettingStructure: 'no-limit',
    maxSeats,
    startingStack: 0,
    blindSchedule: [{ level: 1, smallBlind, bigBlind }],
    personaPolicy: { defaultStyle: 'balanced' },
    ruleSet: {
      streets: ['preflop', 'flop', 'turn', 'river', 'showdown'],
      postingOrder: ['small-blind', 'big-blind'],
      minRaisePolicy: 'double-last-bet',
      showdownOrdering: 'high-card',
      cardDistribution: { holeCardsPerPlayer: 2, burnPerStreet: [1, 1, 1], communityReveal: [0, 3, 1, 1] },
    },
    evaluationPolicy: { engine: 'lookup-table', evaluatorId: 'default', supportsHiLo: false, cacheSize: 1024 },
    autoAdvance: true,
  };
}

function resume(state) {
  return SessionManager.resume(
    {
      sessionId: state.id,
      config: state.config,
      runtimeContext: state.runtimeContext,
      initialSnapshot: state.initialSnapshot,
      events: state.events,
      metrics: state.metrics,
      channels: state.channels,
      hooks: {},
    },
    { now },
  );
}

function serialize(m) {
  const s = m.session;
  return {
    id: s.id,
    config: s.config,
    runtimeContext: s.runtimeContext,
    initialSnapshot: toSnapshotEnvelope(s.initialSnapshot),
    activeSnapshot: s.activeSnapshot,
    metrics: s.metrics,
    channels: s.channels,
    events: m.eventLog,
  };
}

// The actor's chips in on the current street. Posted blinds are not turns in
// the round, so preflop adds them.
function streetBet(snapshot, playerId) {
  const round = snapshot.hand.bettingRounds.at(-1);
  let bet = round ? (deriveRoundComputation(round).contributions.get(playerId) ?? 0) : 0;
  if (snapshot.hand.stage === 'preflop') {
    const { smallBlind, bigBlind } = snapshot.hand.blinds;
    for (const b of [smallBlind, bigBlind]) {
      if (b && b.playerId === playerId && !b.isDead && typeof b.amount === 'number') bet += b.amount;
    }
  }
  return bet;
}

function view() {
  if (!manager) return null;
  const session = manager.session;
  const snap = session.activeSnapshot;
  const tv = selectTableView(session);
  const board = [...(tv.board.flop ?? []), ...(tv.board.turn ? [tv.board.turn] : []), ...(tv.board.river ? [tv.board.river] : [])];
  const folded = new Set();
  for (const r of snap.hand.bettingRounds) for (const t of r.turns) if (t.action?.type === 'fold') folded.add(t.actor);
  return {
    handNumber: tv.handNumber,
    stage: tv.handStage,
    actor: tv.currentActor ?? null,
    button: tv.dealerButton,
    smallBlind: snap.hand.blinds.smallBlind?.playerId ?? null,
    bigBlind: snap.hand.blinds.bigBlind?.playerId ?? null,
    pot: tv.potTotal,
    board,
    holeCards: snap.cards.holeCards,
    seats: tv.seats
      .filter((s) => s.playerId)
      .map((s) => ({
        id: s.playerId,
        seat: s.seatIndex,
        stack: s.stack,
        streetBet: streetBet(snap, s.playerId),
        status: s.isAllIn ? 'all-in' : folded.has(s.playerId) ? 'folded' : s.isActive ? 'active' : 'out',
      })),
    legal: (tv.availableActions ?? []).filter((o) => !o.disabled),
  };
}

async function handle(req) {
  switch (req.op) {
    case 'create': {
      config = makeConfig(req.maxSeats, req.smallBlind, req.bigBlind);
      const seats = req.seats.map((s) => ({ playerId: s.id, seatIndex: s.seat, stack: s.stack }));
      const summary = await generateRandomState({
        config, seats, random: () => 0.5, steps: { min: 0, max: 0 },
        managerOptions: { deck: req.deck?.length ? req.deck : undefined, now },
      });
      manager = resume(summary.session);
      return {};
    }
    case 'act': {
      if (!manager) throw new Error('no hand in progress');
      const tv = selectTableView(manager.session);
      if (tv.currentActor !== req.actor) throw new Error(`not ${req.actor}'s turn (to act: ${tv.currentActor ?? 'nobody'})`);
      let requested;
      switch (req.type) {
        case 'fold':
        case 'check':
          requested = { type: req.type };
          break;
        case 'call': {
          const offered = tv.availableActions.find((o) => o.type === 'call');
          requested = { type: 'call', amount: offered?.amount ?? req.amount };
          break;
        }
        case 'bet':
          requested = { type: 'bet', amount: req.to };
          break;
        case 'raise':
          // As the engine's playground sends it: chips going in now, and the
          // street total.
          requested = { type: 'raise', amount: req.to - streetBetRaw(req.actor), to: req.to };
          break;
        case 'all-in': {
          const offered = tv.availableActions.find((o) => o.type === 'all-in');
          requested = { type: 'all-in', amount: offered?.amount ?? req.amount };
          break;
        }
        default:
          throw new Error(`poker-engine-ts has no ${req.type} action`);
      }
      intents += 1;
      const r = await manager.applyIntent({
        id: `scene-${intents}`, actor: req.actor, requested, origin: 'ui',
        issuedAt: NOW + intents, expectedSnapshotVersion: manager.session.activeSnapshot.index,
      });
      if (r.validation.kind !== 'accepted') throw new Error(`rejected: ${r.validation.reason ?? r.validation.kind}`);
      return {};
    }
    case 'newHand': {
      if (!manager) throw new Error('no table yet');
      await manager.advanceHand(req.deck?.length ? req.deck : undefined);
      // Deal the new hand's hole cards the documented way.
      const summary = await advanceRandomState(serialize(manager), { steps: { min: 0, max: 0 }, resumeOptions: { now } });
      manager = resume(summary.session);
      return {};
    }
    case 'state':
      return {};
    default:
      throw new Error(`unknown op ${req.op}`);
  }
}

// The round-only contribution the engine itself uses for raise sizing.
function streetBetRaw(playerId) {
  const round = manager.session.activeSnapshot.hand.bettingRounds.at(-1);
  return round ? (deriveRoundComputation(round).contributions.get(playerId) ?? 0) : 0;
}

const rl = createInterface({ input: process.stdin });
let queue = Promise.resolve();
rl.on('line', (line) => {
  queue = queue.then(async () => {
    let out;
    try {
      await handle(JSON.parse(line));
      out = { state: view() };
    } catch (e) {
      out = { error: String(e?.message ?? e), state: view() };
    }
    process.stdout.write(JSON.stringify(out) + '\n');
  });
});
