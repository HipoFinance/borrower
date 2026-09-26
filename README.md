# Borrower

Borrower is a utility for TON blockchain validators to request loans from hGRAM: Hipo liquid staking protocol.

If you're a node operator, but you don't have enough GRAM to stake, you're at the right place. With Borrower you can request a loan from [hGRAM](https://github.com/HipoFinance/contract), validate blocks, and earn a reward for your service.

Before moving forward, first read the documentation of [hGRAM contract](https://github.com/HipoFinance/contract).

## Validating Blocks

TON blockchain is a Proof-of-Stake (PoS) blockchain. That means that in order to create new blocks, you don't have to buy expensive hardware and pay a lot of money for electricity to produce lots of hashes, like Bitcoin miners. Instead, you have to stake your money for a fixed period of time and use a generally available server to participate in block creation.

PoS blockchains reward their validators for their service, and in TON, every new block that is created will generate some GRAM that will be distributed between validators. In addition, transaction fees paid by blockchain users will be distributed between validators.

Now, to start validating, you need access to a large sum of GRAM, like 300,000 GRAM or more. If you don't have access to that, you're at the right place. Borrower helps you in requesting a loan from hGRAM treasury. hGRAM treasury is the place where other users put their GRAM to help validators like you, and in return they want a portion of the rewards. We refer to these users as stakers.

If your loan request is accepted, you'll be given at least the requested amount, so that you can use it to participate in elections for the next round, and assuming you win in the election, your node will start to validate blocks for a fixed period of time, like 18 hours.

To prevent validators from doing nasty things to the blockchain, after a round of validation, there is a period of time (like 9 hours) that validators may be punished. This process is done by other validators, and they might propose to punish a rogue validator. So, in order to validate, you have to bring the maximum possible punishment for your requested loan when asking for it. This way, hGRAM won't have to pay the punishment from stakers' pocket. At the time of writing, the maximum punishment is 101 GRAM.

### Competition Between Validators

The treasury is permission-less: anyone can request a loan. When there are more requests than GRAM
to lend, the treasury decides who gets one. This section describes exactly how, so that you can
price a request instead of guessing. The authoritative source is `decide_loan_requests` and
`recover_stake_result` in [the contract](https://github.com/HipoFinance/contract), and its
[integration guide](https://github.com/HipoFinance/contract/blob/main/docs/integration.md).

#### What you bid

A request carries three numbers you choose:

- **`loan`**: how much GRAM you want to borrow. Together with your own `stake` it must reach the
  network's `min_stake` (config 17), though in practice the smallest stake the elector actually
  elects is much higher than that, and a stake below it earns nothing.
- **`min_payment`**: what you promise the pool for the loan. It is best read as a **rate**: see
  *Pricing a bid* below.
- **`max_stake`**: the most your loan will stake in total -- `loan`, plus whatever leftover the
  treasury adds to it, plus your collateral (your own `stake` included) -- or 0 for no cap. See
  *Capping your stake* below.

The **reward share** is not part of the bid. The treasury sets one value for every loan
(`reward_share`, out of 65535, index 26 of `get_treasury_state`), and a request that tries to name
its own is refused. This borrower reads it from the treasury, so there is nothing to configure; an
old `reward_share` or `validator_reward_share` key left in `borrower.yaml` is ignored. It needs a
treasury from 21 September 2026 or later, when the share became the protocol's.

#### How requests are ranked and accepted

1. Requests are ranked on **efficiency**, `min_payment / loan`, with both rounded down first:
   `min_payment` to units of about 1.07 GRAM and `loan` to units of about 1,100 GRAM. On a tie the
   smaller loan goes first. The borrower logs your bid's efficiency when it sends a request.
2. The treasury serves requests in that order. **A request that does not fit in what is left is
   skipped, and the next one is tried**, so a lower-ranked request still wins if it fits. Rank only
   decides the order of service.
3. Whatever is left after the accepted requests is added to them in proportion to their `loan`
   (the *accrued* amount), so the pool is always fully lent.

Requests are public the moment they land, and can be replaced until bidding closes at
`participate_since` (from the treasury's `get_times`). Replacing a request costs another request fee
and keeps your collateral: the borrower sends only the fee and whatever the posted collateral falls
short of. After every send it checks, two minutes later, that the request is actually standing, and
sends again if the treasury refused it -- at most three times a round, and never within three minutes
of the close.

#### Pricing a bid

When a round ends, the pool receives the larger of your `min_payment` and its **contractual share**
of the reward, `reward × (65535 − reward_share) / 65535`. You keep the rest, less the borrower fee
(`borrower_fee`, index 20: a share of your own contractual reward, at least 1 GRAM, sent to the HPO
burner).

- A `min_payment` **below** the pool's contractual share is never paid. It costs you nothing and
  only sets your rank.
- A `min_payment` **above** it is paid out of your reward, and if the reward falls short, out of
  your collateral. The pool never collects more than the reward plus your collateral.
- **`min_payment` is scaled to the whole stake.** When the treasury adds an accrued amount to your
  loan, it scales `min_payment` by `(loan + accrued) / loan`. What you promise is therefore a rate
  on everything your loan stakes, and the efficiency you rank on is exactly that rate. Price it that
  way: a bid priced on leftover you *expect* to receive, but divided by a smaller `loan`, pays that
  same inflated rate on the leftover too.

  This applies from treasury code `f003de4b…`, deployed on 26 September 2026. Before it,
  `min_payment` was not scaled.

  One consequence to price for: the elector pays nothing on stake above its cap (`max_factor` times
  the smallest elected stake), but the treasury scales `min_payment` on everything it lends you. A
  loan that ends up taking most of the pool -- because it is the only one accepted -- can pass that
  cap, and then even a `min_payment` at the pool's contractual share binds. Set `max_stake` to stop
  that (below); without it, if the pool is larger than the cap, keep `min_payment` below
  `cap / pool` of the break-even figure below.

A worked example with the figures of September 2026: a stake earned about **660 GRAM per 1,000,000
staked** per round, and `reward_share` was 1799, so the pool's contractual share was 97.25% of the
reward and the borrower's 2.75%, of which the borrower fee (50%) burned half. On a 1,000,000 GRAM
loan that is a reward of about 660, of which the pool's share is about 642 and the borrower keeps
about 9. A `min_payment` of about **651** is where a borrower breaks even: above it the loan costs
money, below it the promise is only a ranking signal. The borrower logs this rate as
`GRAM per 1,000,000 lent` with every request. Rewards move from round to round (between about 644
and 689 per million over the same period), so a bid priced exactly at break-even loses money in a
round that pays less.

#### Capping your stake

`max_stake` bounds everything your validator stakes: `loan` + accrued + collateral, where collateral
is everything the request leaves with the treasury, your own `stake` included. The treasury stops
adding leftover to your loan at that total, and what it does not add stays in the treasury -- it is
not given to the other borrowers. Set it to the cap you expect the elector to apply to you, about
`max_factor` times the smallest stake it will elect, and you are never lent stake that earns nothing
while `min_payment` is charged on it. With a cap you can price `min_payment` on the loan you
request again. 0 means no cap.

The treasury refuses a `max_stake` below `loan` + collateral, so the borrower checks that before it
sends and says so instead. A changed `max_stake` counts as a changed bid and is re-sent.

`max_stake` exists from the treasury's stake-cap release on, which made it a **required** field of
`request_loan`; the code before that release refuses a request that carries it. The borrower reads
the treasury's code hash and sends the field only once the treasury has the release, so this
version works on both sides of the upgrade. **Borrower v2.0.0 and earlier cannot bid after that
upgrade**: the treasury bounces their requests (the collateral comes back) until you update.

#### Collateral, and a loan that is not elected

With the request you send collateral of at least `min_payment` + the maximum punishment for your
stake (currently 101 GRAM) + 1 GRAM for the burn floor, plus the request fee. The borrower computes
and sends this for you, and refuses to send when the wallet cannot cover it. Collateral comes back
with the loan result, less whatever the round took from it.

If your stake is accepted but not elected, it earns nothing and the pool takes your `min_payment`
(scaled, if the loan accrued) out of your collateral, up to the whole collateral.

## Setup

Rent a server that has the [minimum hardware requirements](https://docs.ton.org/participate/run-nodes/full-node#hardware-requirements).

1. Install [mytonctrl](https://github.com/ton-blockchain/mytonctrl/) in full mode, but after the installation don't create any wallets or pools.

    `mytonctrl` is a tool that installs a TON blockchain full node to validate blocks, configures and starts it. It also helps with upgrading the validator software. To install it on Ubuntu, run its install.sh script like this:

    ```sh
    wget https://raw.githubusercontent.com/ton-blockchain/mytonctrl/master/scripts/install.sh
    sudo bash install.sh -m full -d
    ```

    Note 1: If you want to run the validator on the testnet, don't use the `-d` flag and instead add this flag: `-c https://ton-blockchain.github.io/testnet-global.config.json`.

    Note 2: Follow [mytonctrl installation manual](https://github.com/ton-blockchain/mytonctrl/blob/master/docs/en/manual-ubuntu.md), sections 1 and 2. There is no need to create wallets for other steps of the manual. The borrower needs a sync liteserver, so you may wait at this step for `mytonctrl` to get synced.

    After the installation, run `mytonctrl` executable. Then run the `status` command. Now your node should be syncing or maybe already synced. Just note the "ADNL address of local validator" in the output of the `status` command, since you'll need it to configure Borrower.

    Note 3: If you got an error like "Check `total_wt >= W[a]` failed" when running the status command on the testnet, use `status fast` instead.

2. Install Borrower. Either:

    - download a release from the [releases page](https://github.com/HipoFinance/borrower/releases):
      `borrower-linux-amd64` or `borrower-linux-arm64`, plus `SHA256SUMS`. Check the download and
      install it:

      ```sh
      sha256sum --check --ignore-missing SHA256SUMS
      install -D -m 755 borrower-linux-amd64 ~/go/bin/borrower
      ~/go/bin/borrower -version
      ```

      For a root user that path is `/root/go/bin`.

    - or build from source: install Go 1.26 or later (`snap install go --classic`), then run
      `make install` (or `go install`) in a clone of this repository.

3. Download the `borrower.yaml` template config file from this repository. Copy it to `~/go/bin` alongside the `borrower` executable. Then edit it and set your configuration:

    - `treasury`: Address of the treasury contract.

    - `borrow`: Configuration related to each loan request.

    - `wallet`: Your wallet configuration that is used to send loan requests and the needed GRAM amount.

    - `validator_engine`: Configure your validator here, specifically enter your ADNL address from the `status` command of `mytonctrl`.

    Then check the configuration without sending anything. From the directory holding `borrower.yaml`:

    ```sh
    borrower -dry-run
    ```

    It reads the chain, the validator engine and the wallet once, and logs the loan request it would
    send: the loan, the `min_payment`, the GRAM it would attach, and the bid's rate and efficiency. It
    stops before the validator engine is configured or the wallet sends anything. It exits 0 when it
    would send a request (or yours already stands), 2 when a real run would send none -- inactive,
    wallet too short, bidding closed, a loan under `min_stake` -- and 1 on an error.

4. Install the service file. Copy `borrower.service` to `/etc/systemd/system` and edit it according to your configuration. Then run these one by one:

    ```sh
    sudo systemctl daemon-reload
    sudo systemctl enable borrower.service
    sudo systemctl start borrower.service
    sudo systemctl status borrower.service
    ```

Now the service is installed and will always run. To view its logs use `journalctl -u borrower.service` or `journalctl -u borrower.service -f`.

The service file restarts the borrower 10 seconds after it exits, and runs it with the system
directories read-only and without the ability to gain privileges. The borrower writes nothing to disk,
so this costs nothing; the comments in `borrower.service` say what each line is for.

## Building and Releasing

`make` lists the targets. The ones that matter:

- `make test` runs `go vet`, checks formatting and runs the tests; `make build` builds `bin/borrower`
  for this machine.
- `make dist VERSION=v3.0.0` cross-compiles `dist/borrower-linux-amd64` and
  `dist/borrower-linux-arm64` with `-trimpath`, stamps the version into them (`borrower -version`
  prints it, and the log's first line repeats it), and writes `dist/SHA256SUMS`.

CI runs the tests on every push and pull request. A release is cut by pushing a tag:

```sh
git tag -a v3.0.0 -m "v3.0.0"
git push origin v3.0.0
```

The release workflow refuses a tag that is not on `main`, runs the tests, builds with `make dist`
using the newest stable Go, and publishes a GitHub release with the two binaries and `SHA256SUMS`
(a tag with a `-`, such as `v3.0.0-rc1`, is marked as a prerelease). A binary built any other way
reports its version as `dev`.

## License

MIT
