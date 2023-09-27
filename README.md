# Borrower

Borrower is a utility for TON blockchain validators to request loans from hTON: Hipo liquid staking protocol.

If you're a node operator, but you don't have enough TON to stake, you're at the right place. With Borrower you can request a loan from [hTON](https://github.com/HipoFinance/contract), validate blocks, and earn a reward for your service.

Before moving forward, first read the documentation of [hTON contract](https://github.com/HipoFinance/contract).

## Validating Blocks

TON blockchain is a Proof-of-Stake (PoS) blockchain. That means that in order to create new blocks, you don't have to buy expensive hardware and pay a lot of money for electricity to produce lots of hashes, like Bitcoin miners. Instead, you have to stake your money for a fixed period of time and use a generally available server to participate in block creation.

PoS blockchains reward their validators for their service, and in TON, every new block that is created will generate some Toncoin that will be distributed between validators. In addition, transaction fees paid by blockchain users will be distributed between validators.

Now, to start validating, you need access to a large sum of Toncoin, like 300,000 TON or more. If you don't have access to that, you're at the right place. Borrower helps you in requesting a loan from hTON treasury. hTON treasury is the place where other users put their Toncoin to help validators like you, and in return they want a portion of the rewards. We refer to these users as stakers.

If your loan request is accepted, you'll be given at least the requested amount, so that you can use it to participate in elections for the next round, and assuming you win in the election, your node will start to validate blocks for a fixed period of time, like 18 hours.

To prevent validators from doing nasty things to the blockchain, after a round of validation, there is a period of time (like 9 hours) that validators may be punished. This process is done by other validators, and they might propose to punish a rogue validator. So, in order to validate, you have to bring the maximum possible punishment for your requested loan when asking for it. This way, hTON won't have to pay the punishment from stakers' pocket. At the time of writing, the maximum punishment is 101 TON.

### Competition Between Validators

Since hTON is a permission-less smart-contract, anyone can request a loan from it, and to manage the limited resources of the protocol, loans will be given to validators with best return on investment (RoI).

When requesting a loan, there are a few parameters sent alongside your request, which can determine the winners:

- **Loan Amount**: This is the minimum amount of loan that you want to receive, and indicates that you're not interested in any amount less than it.

- **Validator Reward Share**: Your share of the earned rewards. For example, if you set it at 40%, you'll receive 40% of the rewards and 60% will go to the protocol, to be distributed between stakers.

- **Minimum Payment**: To prevent attacks to the protocol, and to make the competition more fair, validators can set a minimum payment. This amount will be deducted from their returned reward in case their loan is accepted. So, validators can calculate the returned rewards for the round they're participating in, find out how much they'll earn, and set a reasonable amount here to have more opportunity to win.

- Stake Amount: In addition, validators can bring their own Toncoin to the table if they have a substantial amount. This amount will be added to their loan. For example, if you have 100,000 TON, you can then ask for just 200,000 TON and bring your own Toncoin to reach the minimum of 300,000 TON.

When the protocol is deciding on loans, requests are sorted.

1. The sort criteria is first based on the return on the investment. In effect, the ratio of guaranteed return on investment is calculated and loans with more RoI will be accepted first. You may assume the formulae like this:

    > RoI = Minimum Payment / Loan Amount

    So, those with more payment and less loan amount have a higher chance to win. To prevent validators from cheap competition, these amounts are rounded. Minimum payment is rounded to around 1 TON and loan amount is rounded to around 1100 TON.

2. The second criteria is the validator reward share. Here, the validators who take less share of the reward, will have more opportunity.

    Because this value can be specified in the range of 0-255, each step is around 0.4%. So, validators can only compete in this criteria by 0.4% steps.

3. The third criteria is the loan amount itself. Whoever asks for less loan has a better chance of winning.

As a result, loans are given in a competition, and the best return for stakers and validators is incentivized.

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

    - download a pre-built and released version from the releases section of Github. Copy it to this path: `~/go/bin`, for example, if you're using a root user copy it to `/root/go/bin`.

    - or download from the source, install `go` by runnig `snap install go --classic`, and then run `go install` in the downloaded git repository.

3. Download the `borrower.yaml` template config file from this repository. Copy it to `~/go/bin` alongside the `borrower` executable. Then edit it and set your configuration:

    - `treasury`: Address of the treasury contract.

    - `borrow`: Configuration related to each loan request.

    - `wallet`: Your wallet configuration that is used to send loan requests and the needed TON amount.

    - `validator_engine`: Configure your validator here, specifically enter your ADNL address from the `status` command of `mytonctrl`.

    - `tonlib_cli`: Configure `tonlib-cli` here. You may need to build it from the source using `cd /usr/bin/ton && cmake --build . --target tonlib-cli`. You may need to stop `validator.service` before compilation.

4. Install the service file. Copy `borrower.service` to `/etc/systemd/system` and edit it according to your configuration. Then run these one by one:

    ```sh
    sudo systemctl daemon-reload
    sudo systemctl enable borrower.service
    sudo systemctl start borrower.service
    sudo systemctl status borrower.service
    ```

Now the service is installed and will always run. To view its logs use `journalctl -u borrower.service` or `journalctl -u borrower.service -f`.

## License

MIT
