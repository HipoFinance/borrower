# Bidding under the treasury's soft close

**Status:** approved (2026-11-19, after two money audits and a revision)

## Problem

The treasury's soft close (contract spec `docs/specs/2026-11-17-soft-close-four-hours.md`) changes
when and how a loan request can be sent:

- new requests and price changes stop at the close E, normally four hours before
  `participate_since` (D);
- a request in the last 15 minutes before E moves E to 15 minutes after it, never past D;
- between E and D a borrower may only shrink its own request; anything else is refused with 201.

The borrower today:

- sends its configured request when the round opens;
- checks two minutes later that it stands;
- re-sends up to three times a round, never within three minutes of D (`sendMargin`).

Under the new rules, a re-send after E is refused. Rivals can now be answered, but the borrower
never answers.

## In plain terms

Once the treasury moves to the soft close, a loan auction becomes an ascending auction. When
someone outbids you, you have 15 minutes to answer, and the auction ends about four hours before
the election deadline, or later while people keep answering.

This change keeps the borrower simple. By default it still sends its configured bid early, once,
and checks it stands, but now before the new close. An operator who wants to compete turns on
`respond` and sets the most they are willing to promise, `max_min_payment`.

Whenever another request ranks above theirs, the borrower re-bids one step above it:
- up to that ceiling;
- at most `max_responses` times a round.

A rival priced beyond the ceiling is not chased.

Each answer costs another request fee (about 0.725 GRAM at the time of writing).

Nothing else changes: the loan size, the stake message, stake recovery and the floors checks.

The main risks:

- **An answer that lands after the close.** The treasury refuses it and bounces the value, and
  only gas is lost. So the borrower stops sending a minute before the close, and its messages
  expire before it.
- **A rival that pushes you to your ceiling with a request the pool will never lend.** A loan
  larger than the pool is refused at the decision and costs that rival one fee. Set
  `max_min_payment` no higher than you are willing to pay in every round (Open question 2).

No decisions left open.

## Definitions

### Concepts

- **New rules:** apply from the first pass in which the treasury's `get_auction_close` getter
  answers.
  - The getter counts as absent only on exit code 11 (method not found). Any other error is a
    failed read, and the pass retries.
  - Once new rules are seen, the borrower never falls back to the old rules.
  - Under the old rules it behaves as today.
  - Status 2 means nobody may send: the borrower sends nothing.
- **D, E:** `participate_since`, and the effective close returned by `get_auction_close`. The
  borrower uses min(E, D) as the last moment for any request.
- **Outranked:** another request is decided before ours. That means a higher sort key, or an
  equal key and a lower address. Of two requests at the same efficiency, the smaller loan ranks
  higher.
- **Answer:** a replacement of our standing request with a higher `min_payment`, same loan, sent
  before E.

### Formula parameters

| Symbol | Meaning | Unit | Range / rounding |
|---|---|---|---|
| e(P, L) | efficiency, ((P >> 30) · 1000) / max(1, L >> 40) | efficiency unit | integer, floor |
| L | the configured loan | nanoGRAM | fixed for the round |
| P | our standing `min_payment` | nanoGRAM | |
| P_max | `max_min_payment` | nanoGRAM | ≥ `min_payment` |
| c | e(P_max, L) | efficiency unit | floor |
| e_r | the efficiency of a request that outranks ours | efficiency unit | integer |
| a | the answer's efficiency | efficiency unit | a = 1 + max{ e_r : e_r + 1 ≤ c } |
| P₁ | the answer's `min_payment` | nanoGRAM | smallest P with e(P, L) ≥ a, on a 2^30 boundary |

If no outranking request has e_r + 1 ≤ c, there is no answer. Because a > e_r, the answer outranks
that request whatever its loan size.

## Decision

- **New `borrow` keys:**

  | Key | Default | Meaning |
  |---|---|---|
  | `respond` | `no` | whether to answer outbids at all |
  | `max_min_payment` | equal to `min_payment` | the ceiling; with the default, nothing is ever raised |
  | `max_responses` | `10` | answers per round |

  - `min_payment` stays the starting bid.
  - The ceiling is an amount for the configured loan. The public borrower never changes its loan,
    so an amount is unambiguous.
- **Trigger:** answer whenever a request outranks ours, at one efficiency step above the highest
  outranking request the ceiling can beat.
- **Schedule:**
  - with `respond: yes`, check every 60 s from our first send until E − 15 min, then every 15 s
    until E;
  - without it, the existing two-minute standing check.
- **The last moment:** under new rules, nothing (first send, re-send or answer) is sent at or after
  C − 60 s, where C is min(E, D), or D while the round has no request.
  - Every new-rules message has `valid_until` = min(now + 180 s, C − 5 s), where C is min(E, D), or
    D under status 0, so a message delayed in delivery expires instead of landing late.
  - Under status 0 (no request yet), the treasury accepts a first bid until D, so the cut-off
    counts from D.
  - Under old rules, `sendMargin` (3 min before D) is unchanged.
- **Own other validators:** a new `ignore` list holds the operator's other borrower wallets. Their
  requests are never answered, so two instances of one operator can't bid each other up.
- **Never come down within a round:** an answer never lowers `min_payment`.
- **The ceiling caps `min_payment`, not what is paid.** When the treasury adds leftover to the
  loan, it scales what is owed by (loan + added) / loan. The README says so next to
  `max_min_payment`.
- **No shrink after the close.** A loan that no longer fits after withdrawals is skipped, as
  today.
- **Reviewed changes (2026-11-19):**
  - an answer that shows in the next view is a standing request, not a refused send, so answers do not
    use up the three re-sends;
  - valid_until is read back from the signed message and rebuilt if later than C − 5 s;
  - after C the borrower keeps polling every 15 s until D, re-reading the getter;
  - collateral is sized for the min_payment actually sent;
  - the flag that old rules are never used again is per process, and a restart re-learns it;
  - the restart recount stops at 2 × the round length before the round, and a failed recount is retried on the
    next pass, and no answer is sent until it works;
  - an answer is counted when it is sent, before waiting for the transaction.
- **Rollout:**
  - released before the treasury upgrade;
  - it switches per round on the getter, so an operator can update at any time before;
  - it continues to bid correctly across the upgrade.

## Changes

- **`borrower/config.go`:** `Respond bool`, `MaxMinPayment string`, `MaxResponses *int`,
  `Ignore []string`, with the defaults above. Validation: `max_min_payment ≥ min_payment`, and
  `max_responses ≥ 0`.
- **New `borrower/answer.go`:**
  - `Outranked(ourKey, ourAddr, requests)`;
  - `Answer(P, L, c, requests)` returns P₁ or nothing. Both are pure and table-tested.
  - `SortKey` is ported from the same formula the treasury uses (`request_sort_key`).
- **`borrower/process.go`:**
  - read `get_auction_close`, and pick old or new rules per round;
  - the send margin becomes min(E, D) − 60 s under new rules, with `valid_until` as above;
  - the never-fall-back record and the status 2 rule;
  - with `respond`, the poll schedule and the answer;
  - the answer count is kept per round in memory, and recounted from the treasury's
    transactions on restart (Open question 1).
- **`borrower.yaml`:** the three keys, with comments.
- **`README.md`:**
  - "Competition Between Validators": the close, extensions, shrink-only after the close, and
    answers;
  - the paragraph on replacing a request until `participate_since` is updated.

## Invariants

- **No answer exceeds the ceiling:** P₁ ≤ P_max, and e(P₁, L) ≤ c.
- **No more than `max_responses` answers in a round.** The first send and re-sends after a refusal
  don't count.
- **The loan, `max_stake` and the stake message of an answer equal the standing request's.** Only
  `min_payment` and the collateral that covers it change. The value sent is the fee plus whatever
  the posted collateral falls short of.
- **Under new rules no request is sent at or after min(E, D) − 60 s,** and none can land after
  min(E, D) − 5 s.
- **A request from an `ignore` address is never answered.**
- **Under old rules, behaviour is unchanged.**
- **Every request is sent bounceable**, so a refused one returns its value.

## Compatibility

- **Config:** an existing `borrower.yaml` loads unchanged and behaves as before (`respond: no`).
- **Treasury:**
  - works with the live code (old rules) and the soft-close code (new rules), per round;
  - a borrower that is not updated keeps sending at round open, which is long before E, so it
    keeps working. It just never answers, and a re-send after E is refused.
- **Message schemas:** unchanged.

## Acceptance criteria

- [x] `Answer` table tests (`answer_test.go`):
  - no outranking request → nothing;
  - an outranking request at e_r with e_r + 1 ≤ c → a = e_r + 1, and P₁ is the smallest such
    `min_payment`;
  - an outranking request at c → nothing;
  - two outranking requests, one beatable and one not → the beatable one + 1;
  - an equal-efficiency smaller loan outranks us and is answered at +1.
- [x] `Outranked`: higher key; equal key and lower address; equal key and higher address (not
  outranked).
- [x] With `respond: no` or `max_min_payment` equal to `min_payment`, no answer is ever sent.
- [x] After `max_responses` answers, an outbid is logged and not answered.
- [x] Under new rules, a send planned at min(E, D) − 59 s is skipped and logged, and every
  new-rules message carries `valid_until` = min(now + 180 s, C − 5 s), C = min(E, D), or D under
  status 0.
- [x] A request from an `ignore` address that outranks us is not answered.
- [x] A liteserver error on the getter is not read as "absent". After new rules are seen, an error
  never selects old rules.
- [x] Under old rules (getter absent), the existing tests pass unchanged and `sendMargin` is 3 min
  before D.
- [x] The request message is built bounceable (a test on the built message).
- [x] `go test ./...` and `go vet ./...` pass.
- [ ] Sandbox end-to-end against the soft-close treasury: a rival outbids in the zone, the
  borrower answers within the window, and the answer is accepted and moves E.

## Out of scope

- Shrinking after the close, a priority group of validators, and pricing from the measured yield.
  Those belong to an operator's own tooling.
- Changing the configured loan in response to rivals.

## Open questions

None. Decided 2026-11-19:

1. **Counting answers across a restart:** on start, the count is rebuilt from the wallet's
   transactions to the treasury for this round, so a restart does not reset `max_responses`.
2. **A rival that can't be lent:** documented, not simulated. The README tells operators to set
   `max_min_payment` no higher than the price they would pay in any round, because a rival can push
   them there for the cost of one fee.
