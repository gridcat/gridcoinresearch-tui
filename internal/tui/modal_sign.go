package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// runSign mirrors runSend's lifecycle for the signmessage RPC. needsUnlock
// is the conjunction of "wallet is encrypted" AND "wallet is currently
// locked". An unencrypted wallet, or one the user has already unlocked
// for staking, never gets the walletpassphrase / walletlock pair sent at
// it. We only re-lock when WE were the ones who unlocked, so we don't
// trample on the user's existing unlock window.
func runSign(c *rpc.Client, addr, message, passphrase string, needsUnlock bool) tea.Cmd {
	return func() tea.Msg {
		if needsUnlock {
			if err := c.WalletPassphrase(passphrase, 30); err != nil {
				return signResultMsg{err: fmt.Errorf("unlock: %w", err)}
			}
		}
		sig, err := c.SignMessage(addr, message)
		if needsUnlock {
			_ = c.WalletLock()
		}
		return signResultMsg{sig: sig, err: err}
	}
}

// openSignModal resets the sign wizard, pre-filling the address from the
// currently selected entry in the My Addresses panel when that panel has
// focus. Pre-filling makes the common case (sign with one of my own
// addresses) zero-friction; falling back to an empty field keeps the
// modal usable when triggered from the tx panel or before addresses
// have loaded.
//
// needsUnlock follows the same UnlockedUntil tri-state contract used by
// the send wizard: nil = unencrypted, *v == 0 = encrypted+locked,
// *v > 0 = encrypted+already unlocked. We only ever prompt for a
// passphrase in the second case.
func (m *Model) openSignModal() {
	m.mode = modeSign
	m.sign = signState{
		step:        signStepAddress,
		address:     m.sign.address,
		message:     m.sign.message,
		passphrase:  m.sign.passphrase,
		needsUnlock: m.wallet.IsLocked(),
	}
	m.sign.address.SetValue("")
	m.sign.message.SetValue("")
	m.sign.passphrase.SetValue("")

	// Pre-fill from the highlighted address when the addresses panel is
	// focused and has a selection. Skip straight to the message step in that
	// case so the user doesn't have to press enter on a field that is
	// already correct.
	if sel := m.selectedAddress(); m.focusedArea == focusAddr && sel != nil {
		m.sign.address.SetValue(sel.Address)
		m.sign.step = signStepMessage
		m.sign.message.Focus()
		return
	}
	m.sign.address.Focus()
}

// handleSignKey is the sign-wizard's input handler. State machine: the
// current m.sign.step decides which keys do what. Mirrors handleSendKey,
// minus the amount/balance check and the confirm step (signing has no
// fund risk and nothing to broadcast).
func (m Model) handleSignKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.sign.blurAll()
		return m, nil
	}
	if m.sign.busy {
		return m, nil // ignore input while signmessage RPC is in flight
	}
	switch m.sign.step {
	case signStepAddress:
		if key == "enter" {
			if v := strings.TrimSpace(m.sign.address.Value()); v != "" {
				m.sign.errMsg = ""
				m.sign.address.Blur()
				m.sign.step = signStepMessage
				m.sign.message.Focus()
				return m, nil
			}
			m.sign.errMsg = "address required"
			return m, nil
		}
		var cmd tea.Cmd
		m.sign.address, cmd = m.sign.address.Update(msg)
		return m, cmd
	case signStepMessage:
		if key == "enter" {
			if m.sign.message.Value() == "" {
				m.sign.errMsg = "message cannot be empty"
				return m, nil
			}
			m.sign.errMsg = ""
			m.sign.message.Blur()
			if m.sign.needsUnlock {
				m.sign.step = signStepPassphrase
				m.sign.passphrase.Focus()
				return m, nil
			}
			// Wallet is unencrypted or already unlocked, so no passphrase needed.
			m.sign.busy = true
			return m, runSign(m.rpc, m.sign.address.Value(),
				m.sign.message.Value(), "", false)
		}
		if key == "backspace" && m.sign.message.Value() == "" {
			m.sign.step = signStepAddress
			m.sign.message.Blur()
			m.sign.address.Focus()
			return m, nil
		}
		var cmd tea.Cmd
		m.sign.message, cmd = m.sign.message.Update(msg)
		return m, cmd
	case signStepPassphrase:
		if key == "enter" {
			if m.sign.passphrase.Value() == "" {
				m.sign.errMsg = "passphrase required"
				return m, nil
			}
			m.sign.errMsg = ""
			m.sign.busy = true
			return m, runSign(m.rpc, m.sign.address.Value(),
				m.sign.message.Value(), m.sign.passphrase.Value(), true)
		}
		var cmd tea.Cmd
		m.sign.passphrase, cmd = m.sign.passphrase.Update(msg)
		return m, cmd
	case signStepResult:
		// Any key dismisses the result screen.
		m.mode = modeDashboard
		m.sign.blurAll()
		return m, nil
	}
	return m, nil
}

// ---- Edit-label modal -------------------------------------------------

// renderSignModal walks the sign-message wizard. Layout invariant: from
// the message step onwards, the chosen signing address is rendered as a
// persistent "Signing as: …" header at the top of the modal so the user
// can never sign, or read a signature, without seeing which key was
// used. The address-input step suppresses the header because the input
// field itself is the source of truth there.
func (m Model) renderSignModal() string {
	var body string
	switch m.sign.step {
	case signStepAddress:
		body = "Address to sign with:\n\n" + m.sign.address.View()
		if m.sign.errMsg != "" {
			body += "\n\n" + theme.Bad.Render(m.sign.errMsg)
		} else {
			body += "\n\n" + theme.Muted.Render("enter to continue · esc to cancel")
		}
	case signStepMessage:
		body = "Message:\n\n" + m.sign.message.View()
		if m.sign.errMsg != "" {
			body += "\n\n" + theme.Bad.Render(m.sign.errMsg)
		} else {
			body += "\n\n" + theme.Muted.Render("enter to sign · backspace to go back · esc to cancel")
		}
	case signStepPassphrase:
		body = "Wallet is locked. Passphrase:\n\n" + m.sign.passphrase.View()
		if m.sign.errMsg != "" {
			body += "\n\n" + theme.Bad.Render(m.sign.errMsg)
		} else {
			body += "\n\n" + theme.Muted.Render("enter to sign · esc to cancel")
		}
	case signStepResult:
		if m.sign.resultErr != "" {
			// The signmessage / unlock RPC error is daemon-authored text,
			// sanitized like every other daemon string.
			body = theme.Bad.Render("sign failed") + "\n\n" + format.SanitizeTerminal(m.sign.resultErr)
		} else {
			// The signature is a daemon response string; a legitimate one is
			// pure base64, so sanitizing is a no-op unless something hostile
			// snuck in. The message is the user's own typed input, left as-is.
			body = theme.Good.Render("signed ✓") + "\n\n" +
				theme.Label.Render("Message:") + "\n" + m.sign.message.Value() + "\n\n" +
				theme.Label.Render("Signature (base64):") + "\n" + format.SanitizeTerminal(m.sign.resultSig)
		}
		body += "\n\n" + theme.Muted.Render("press any key to close")
	}
	if m.sign.busy {
		body += "\n\n" + theme.Muted.Render("signing…")
	}

	// From the message step onwards, surface the signing address so it is
	// always visible. Skipping it on signStepAddress avoids a redundant
	// echo of the input field one line below.
	header := ""
	if m.sign.step != signStepAddress {
		addr := m.sign.address.Value()
		if addr == "" {
			addr = theme.Muted.Render("(no address set)")
		} else {
			addr = theme.Accent.Render(addr)
		}
		header = theme.Label.Render("Signing as: ") + addr + "\n\n"
	}

	// Default width is comfortable for the input steps. On the result
	// step we expand to whatever the signature needs so it fits on a
	// single uninterrupted line, otherwise lipgloss wraps it inside the
	// modal and a mouse selection drags the right-side border in with
	// the copied text. 6 = 2 border + 4 padding(1,2). Cap to the
	// terminal width so we still render cleanly on narrow terminals
	// (signature will wrap there as a last resort, but most terminals
	// are wide enough).
	modalWidth := 72
	if m.sign.step == signStepResult && m.sign.resultSig != "" {
		if needed := len(m.sign.resultSig) + 6; needed > modalWidth {
			modalWidth = needed
		}
	}
	if max := m.width - 2; modalWidth > max && max > 0 {
		modalWidth = max
	}

	modal := ui.ModalBox(modalWidth, "Sign message", header+body)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// onSignResultMsg handles signResultMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onSignResultMsg(msg signResultMsg) (tea.Model, tea.Cmd) {
	m.sign.busy = false
	m.sign.step = signStepResult
	if msg.err != nil {
		m.sign.resultErr = msg.err.Error()
	} else {
		m.sign.resultSig = msg.sig
	}
	return m, nil
}

// signStep is the state-machine step inside the sign-message modal. The
// passphrase step is skipped entirely when the wallet is unencrypted or
// already unlocked. See openSignModal / runSign.
type signStep int

const (
	signStepAddress    signStep = iota // type / pre-fill the signing address
	signStepMessage                    // type the message to sign
	signStepPassphrase                 // only used when the wallet is encrypted + locked
	signStepResult                     // show signature or error
)

// signState is the live state of the sign-message modal. resultSig holds the
// base64 signature returned by signmessage on success.
type signState struct {
	step        signStep
	address     textinput.Model
	message     textinput.Model
	passphrase  textinput.Model
	needsUnlock bool   // true if the daemon says the wallet is currently locked
	busy        bool   // true while the signmessage RPC is running
	errMsg      string // per-step validation error
	resultSig   string // populated in signStepResult on success
	resultErr   string // populated in signStepResult on failure
}

func (s *signState) blurAll() {
	s.address.Blur()
	s.message.Blur()
	s.passphrase.Blur()
}
