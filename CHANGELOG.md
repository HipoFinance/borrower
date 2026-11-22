# Changelog

Newest first. What changed, why, and what was declined or left open.

## v2.4.0 (2026-11-22) — Bidding under the treasury's soft close

The treasury's soft close ends the loan auction about four hours before `participate_since`. A
request in the last 15 minutes before the close moves the close 15 minutes later, never past
`participate_since`. After the close, a borrower can only shrink its own request. This release
makes the borrower bid correctly under those rules, and lets an operator answer outbids. The spec
is `specs/soft-close-answers.md`.

- **Works with both treasury versions.** The new rules apply once the treasury's `get_auction_close`
  answers. Only exit code 11 means the getter is absent. Against today's treasury, the borrower
  behaves as before.
- **Sends stop before the close.** Nothing is sent within 60 s of the close, and every message
  expires at most 5 s before it. The expiry is checked on the signed message.
- **Optional answers.** These `borrow` keys are new:
  - `respond` (default `no`);
  - `max_min_payment` (default: `min_payment`, so nothing is raised);
  - `max_responses` (default 10);
  - `ignore` (the operator's other borrower wallets).

  With `respond: yes`, a request that ranks above yours is answered at one efficiency step above
  it, up to the ceiling. The answer count is rebuilt from the wallet's transactions after a
  restart. An operator who doesn't change the yaml gets exactly the previous behaviour.
- **Setup:** the README now says to update to v2.4.0 before the treasury's soft-close upgrade, since
  v2.3.0 and earlier may send after the close and lose the round (the collateral is bounced back).
- **Declined:** shrinking after the close, and estimating whether a rival's loan can actually be
  lent. A rival can push you to `max_min_payment` with a request the pool will never lend, so set
  that ceiling no higher than you would pay in any round. The README says so.
