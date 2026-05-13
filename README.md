# gridcoinresearch-tui

![example](https://salmon-ruling-marlin-296.mypinata.cloud/ipfs/bafybeiedy7pnhkxqoankdveazxyfsf6omdpvglzllj5svfpmtsj3sf6ply)

A tiny, full-screen terminal dashboard for a running [Gridcoin Research](https://gridcoin.us) wallet daemon (`gridcoinresearchd`). One static binary, no runtime dependencies.

Shows:

- **Balance** (confirmed / unconfirmed / immature)
- **Staking** status and difficulty
- **Wallet lock** state, with the unlock countdown ticking down live
- **Current block height**
- **Your wallet addresses** with labels and amounts received
- **Recent transactions** with a readable status (`upcoming` / `incoming` / `sending` / `confirmed` / `stake`)
- **Transaction details** (full txid, address, fee, block hash, timestamps). Press `enter` on the selected row.
- **Send GRC** to any address, with pre-flight address validation and a wallet unlock prompt only when needed
- **Sign messages** with any address you own (proves control of the address)
- **Live config panel.** Edit network, host, port, credentials, and refresh interval at runtime, without restarting.

Supports both mainnet and testnet via a CLI flag.

## Install

Download a pre-built binary from the [Releases](https://github.com/gridcat/gridcoinresearch-tui/releases) page. Every release is notarised on the Gridcoin blockchain via [gridcoin-stamp-action](https://github.com/gridcat/gridcoin-stamp-action). The `checksums.txt` hash is recorded on-chain, so you can independently verify the artifacts.

```sh
curl -sSL https://github.com/gridcat/gridcoinresearch-tui/releases/latest/download/gridcoinresearch-tui_linux_amd64.tar.gz | tar -xz
./gridcoinresearch-tui
```

Or build from source:

```sh
git clone https://github.com/gridcat/gridcoinresearch-tui
cd gridcoinresearch-tui
make build
./gridcoinresearch-tui
```

## Usage

### Local (same machine as the wallet)

Zero config. Reads `~/.GridcoinResearch/gridcoinresearch.conf`, connects to `127.0.0.1`:

```sh
./gridcoinresearch-tui              # mainnet
./gridcoinresearch-tui --testnet    # testnet (reads ~/.GridcoinResearch/testnet/gridcoinresearch.conf)
```

### Remote (laptop → server)

No local Gridcoin install required:

```sh
./gridcoinresearch-tui \
  --rpc-host node.example.com \
  --rpc-port 15715 \
  --rpc-user myuser \
  --rpc-password mypass
```

Or via env vars (recommended for the password):

```sh
export GRC_RPC_HOST=node.example.com
export GRC_RPC_USER=myuser
export GRC_RPC_PASSWORD=mypass
./gridcoinresearch-tui
```

### All flags

| Flag | Env var | Default | Notes |
|------|---------|---------|-------|
| `--mainnet` | — | on | Default network. Mutually exclusive with `--testnet`. |
| `--testnet` | — | off | Use testnet conf path and default port. |
| `--rpc-host HOST` | `GRC_RPC_HOST` | `127.0.0.1` | |
| `--rpc-port PORT` | `GRC_RPC_PORT` | `15715` mainnet / `25715` testnet | |
| `--rpc-user USER` | `GRC_RPC_USER` | empty | If both user and password are empty, no `Authorization` header is sent. |
| `--rpc-password PASS` | `GRC_RPC_PASSWORD` | empty | Prefer env var over flag to keep it out of `ps`. If `--rpc-user` is set but no password is resolved, you'll be prompted at startup (input is masked). |
| `--conf PATH` | — | `~/.GridcoinResearch[/testnet]/gridcoinresearch.conf` | Best-effort; missing file is fine. |
| `--refresh DUR` | — | `10s` | How often to poll the daemon. |

Flags accept both single- and double-dash forms (`-testnet` and `--testnet` are equivalent), to match Go's standard flag parser. `--help` prints the single-dash form by convention.

Resolution order, highest wins: flag, then env var, then conf file, then the built-in default.

## Keybindings

| Key | Action |
|-----|--------|
| `↑` / `↓` / `k` / `j` | Move the transaction cursor |
| `enter` | Open details for the selected transaction |
| `g` / `G` / `home` / `end` | Jump to first / last transaction |
| `s` | Open the send dialog |
| `m` | Open the sign-message dialog (pre-fills the address when the addresses panel is focused) |
| `c` | Open the live config panel |
| `r` | Force a refresh now |
| `tab` / `shift+tab` | Navigate fields inside a modal |
| `esc` | Close a modal |
| `q` / `ctrl+c` | Quit |

Inside the send and config modals the focused field is marked with `▸ `, so you can still tell what is active on terminals without colour support. The selected transaction row gets the same marker.

## Addresses

The "My Addresses" panel lists every address `listreceivedbyaddress` returns for your wallet, including ones that have never received any coins. Addresses are printed at full length, so you can select them with your terminal's native mouse selection and copy them the usual way. On small terminals the panel is capped so it cannot push the transactions list off screen. A `+N more` line appears when addresses don't fit; resize the window taller to see all of them.

## Sign messages

Press `m` to sign a message with one of your wallet's addresses. Anyone can verify the resulting base64 signature with `gridcoinresearchd verifymessage <address> <signature> <message>`. A successful verification proves you hold the private key for that address.

If the addresses panel is focused (`tab` to switch), the highlighted address is pre-filled. The chosen address stays at the top of the modal on every step, so you always know which key is signing.

The wallet is only unlocked when it has to be. An unencrypted wallet, or one you have already unlocked yourself (e.g. for staking), skips the passphrase prompt entirely. When the TUI does the unlock itself, it re-locks immediately after the signature.

## Config panel

Edits in the config panel are session-only. They apply immediately (the RPC client is rebuilt against the new endpoint and a fresh fetch runs), but they are not written to disk. Next launch re-resolves from flags, env, and conf as usual. Toggling the network auto-updates the port field if it still held the old network's default, so you don't need to remember port numbers.

## Security notes

- The TUI never stores your wallet passphrase. It is held in memory only for the duration of a single `sendtoaddress` call, then the wallet is immediately re-locked via `walletlock`.
- Pass `--rpc-password` via env var, not the command line. Flags are visible in `ps`. Or omit it entirely: when `--rpc-user` resolves but no password does, the TUI prompts for the password at startup with masked input (skipped on non-interactive stdin).
- This tool talks plain HTTP JSON-RPC. Do not expose your daemon's RPC port over the public internet. Use an SSH tunnel for remote access:
  ```sh
  ssh -L 15715:127.0.0.1:15715 user@node.example.com
  ./gridcoinresearch-tui --rpc-user X --rpc-password Y
  ```

## License

MIT

---

<p align="center">Made with ❤️ by @gridcat</p>
