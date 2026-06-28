# gridcoinresearch-tui

![example](https://salmon-ruling-marlin-296.mypinata.cloud/ipfs/bafybeiedy7pnhkxqoankdveazxyfsf6omdpvglzllj5svfpmtsj3sf6ply)

A tiny, full-screen terminal dashboard for a running [Gridcoin Research](https://gridcoin.us) wallet daemon (`gridcoinresearchd`). One static binary, zero runtime dependencies.

Shows:

- **Balance** (confirmed / unconfirmed / immature / total, plus an immature stake row while a stake is maturing)
- **Staking** status and difficulty
- **Wallet lock** state with live unlock countdown
- **Current block height**
- **Your wallet addresses**, grouped into Mine / Others / All tabs, with labels and amounts received, and any address you do not actually own flagged so you never copy it by mistake
- **Recent transactions** with human-readable status (`upcoming` / `incoming` / `sending` / `confirmed` / `stake`)
- **Transaction details** (full txid, address, fee, block hash, timestamps), press `enter` on a selected row
- **Send GRC** to any address, with pre-flight address validation and on-demand wallet unlock
- **Sign messages** with any address you own (proves control of the address)
- **Live config panel**, edit network / host / port / credentials / refresh interval at runtime without restarting

Supports both **mainnet** and **testnet** via a CLI flag.

## Install

Download a pre-built binary from the [Releases](https://github.com/gridcat/gridcoinresearch-tui/releases) page. Every release is notarised on the Gridcoin blockchain via [gridcoin-stamp-action](https://github.com/gridcat/gridcoin-stamp-action), the `checksums.txt` hash is recorded on-chain so you can independently verify the artifacts.

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

Zero config, reads `~/.GridcoinResearch/gridcoinresearch.conf`, connects to `127.0.0.1`:

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
| `--debug-log PATH` | — | empty | Redirect stderr to a file so crash dumps land there instead of wrecking the terminal. See [Troubleshooting](#troubleshooting). |

Flags accept both single- and double-dash forms (`-testnet` and `--testnet` are equivalent), to match Go's standard flag parser. `--help` prints the single-dash form by convention.

Resolution order, highest wins: **flag → env var → conf file → built-in default**.

## Keybindings

| Key | Action |
|-----|--------|
| `↑` / `↓` / `k` / `j` | Move the cursor in the focused panel |
| `pgup` / `pgdn` / `ctrl+u` / `ctrl+d` | Scroll the focused panel by a page |
| `←` / `→` / `h` / `l` | Pan the My Addresses panel sideways when a row is too wide to fit |
| `enter` | Open details for the selected transaction |
| `g` / `G` / `home` / `end` | Jump to first / last row in the focused panel |
| `1` / `2` / `3` | Filter the My Addresses panel: Mine / Others / All |
| `+` / `=` / `-` | Grow / shrink the My Addresses panel (moves the split with the transactions list) |
| `0` | Reset the panel split to its automatic size |
| `s` | Open the send dialog |
| `m` | Open the sign-message dialog (pre-fills the address when the addresses panel is focused) |
| `e` | Edit the label of the selected address (only when the My Addresses panel is focused) |
| `c` | Open the live config panel |
| `r` | Force a refresh now |
| `tab` | Switch focus between the transactions and My Addresses panels |
| `tab` / `shift+tab` | Navigate fields inside a modal |
| `?` | Open the help overlay (all keys and what they do) |
| `esc` | Close a modal |
| `q` / `ctrl+c` | Quit |

Inside the send and config modals the focused field is marked with `▸ ` so you can tell what is active on terminals without colour support. The selected transaction row gets the same marker.

## Addresses

The "My Addresses" panel lists every address `listreceivedbyaddress` returns for your wallet, including ones that have never received any coins. Each row shows the address plus its label, watch-only flag, and amount received. Some rows are flagged `⚠ not yours` in red: these are addresses you have only labelled but do not actually own (for example, someone else's address you saved as a send target). They turn up here because `listreceivedbyaddress` hands back your whole address book rather than just your own keys; the flag comes straight from the daemon's own `validateaddress` `ismine` answer, so you never copy a foreign address thinking it is one of yours. Those foreign rows live under the **Others** tab: the panel opens on **Mine** (your own addresses, plus any whose ownership the daemon has not resolved yet) so they don't clutter the common case, and **All** shows everything together. Press `1`, `2`, `3` to switch tabs; each shows its own count in the header. Rows that are too wide for the panel are clipped rather than wrapped (a muted `‹`/`›` marks hidden content); focus the panel with `tab` and use `←`/`→` to pan sideways and read the rest. Press `e` on the selected address to set or change its label (an empty value clears it); the change is written to the wallet via `setaccount` and shown after the next refresh. One quirk worth knowing: when you relabel an address that is its account's current receiving address, gridcoinresearchd also generates a fresh replacement address that keeps the old label. That is Gridcoin's legacy account system rather than anything the TUI does (the Qt wallet sidesteps it only by setting labels in-process, and Gridcoin exposes no label RPC that skips the behaviour); no coins are affected, you simply end up with one extra address. At normal terminal widths the address itself always fits, so you can still mouse-select and copy it with your terminal's native shortcut. The panel shares vertical space with the transactions list: it opens at about a third of what's available, with a `current/total` indicator in its header when it can't show every row at once. Press `+`/`-` to grow or shrink that split and `0` to snap back to the automatic size; once you have resized it the panel holds its height (padding with blank rows) so it stays put as you switch tabs, and it can never squeeze the transactions list below three rows. Resizing the terminal taller gives both panels more room.

## Sign messages

Press `m` to sign a message with one of your wallet's addresses. The resulting base64 signature can be verified by anyone via `gridcoin-cli verifymessage <address> <signature> <message>`, proving you hold the private key for that address.

If the addresses panel is focused (`tab` to switch), the highlighted address is pre-filled. The chosen address stays visible at the top of the modal on every step, so you always know which key the signature is being produced with.

The wallet is only unlocked when it has to be, an unencrypted wallet, or one you have already unlocked yourself (e.g. for staking), skips the passphrase prompt entirely. When the TUI did do the unlock, it re-locks immediately after the signature is produced.

## Config panel

Edits in the config panel are **session-only**, they apply immediately (the RPC client is rebuilt against the new endpoint and a fresh fetch runs) but are not written to disk. Next launch re-resolves from flags/env/conf as usual. Toggling the network auto-updates the port field if it still held the old network's default, so you don't need to remember port numbers.

## Security notes

- The TUI never stores your wallet passphrase. It is held in memory only for the duration of a single `sendtoaddress` call, then the wallet is immediately re-locked via `walletlock`.
- Pass `--rpc-password` via env var, not the command line, flags are visible in `ps`. Or omit it entirely: when `--rpc-user` resolves but no password does, the TUI prompts for the password at startup with masked input (skipped on non-interactive stdin).
- This tool talks plain HTTP JSON-RPC. Do not expose your daemon's RPC port over the public internet. Use an SSH tunnel for remote access:
  ```sh
  ssh -L 15715:127.0.0.1:15715 user@node.example.com
  ./gridcoinresearch-tui --rpc-user X --rpc-password Y
  ```

## Troubleshooting

If the TUI ever dies after a long run, it leaves your terminal in a weird state: you type but nothing echoes, even though the shell still runs what you type, often after a wall of Go stack-trace text. That means it hit a fatal Go runtime error. That kind of error exits without running the normal terminal-restore, so to get a sane shell back, run `reset` (or `stty sane`).

To capture what actually happened so it can be fixed, start the TUI with `--debug-log`:

```sh
./gridcoinresearch-tui --debug-log ~/grctui-crash.log
```

That points the program's stderr at the file, so the crash dump lands there instead of being painted onto (and lost with) the full-screen display. After a crash, attach that file to a bug report. On Linux the redirect also captures fatal runtime errors; on other platforms it captures the program's own error output.

## License

MIT

---

<p align="center">Made with ❤️ by @gridcat</p>
