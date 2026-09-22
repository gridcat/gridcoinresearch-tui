package tui

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

func validateAddr(c *rpc.Client, addr string) tea.Cmd {
	return func() tea.Msg {
		v, err := c.ValidateAddress(addr)
		return validateMsg{v, err}
	}
}

// runSend performs the send-wizard's final step: unlock the wallet (if
// needed), broadcast the transaction, and ALWAYS re-lock before returning.
// The _ = c.WalletLock() pattern discards the return value on purpose:
// we don't want a re-lock failure to mask a successful send.
func runSend(c *rpc.Client, addr string, amount float64, passphrase string, needsUnlock bool) tea.Cmd {
	return func() tea.Msg {
		if needsUnlock {
			if err := c.WalletPassphrase(passphrase, 30); err != nil {
				return sendResultMsg{err: fmt.Errorf("unlock: %w", err)}
			}
		}
		txid, err := c.SendToAddress(addr, amount)
		if needsUnlock {
			// Best-effort re-lock; don't mask the real error if send succeeded.
			_ = c.WalletLock()
		}
		return sendResultMsg{txid: txid, err: err}
	}
}

// openSendModal resets the send wizard and focuses the address field. It
// preserves the existing textinput.Model instances so their placeholder /
// width / mask settings survive.
func (m *Model) openSendModal() {
	m.mode = modeSend
	m.send = sendState{
		step:            sendStepAddress,
		recipientCursor: 0,
		address:         m.send.address,
		amount:          m.send.amount,
		passphrase:      m.send.passphrase,
		needsUnlock:     m.wallet.IsLocked(),
	}
	m.send.address.SetValue("")
	m.send.amount.SetValue("")
	m.send.passphrase.SetValue("")
	m.send.address.Focus()
}

// handleSendKey is the send-wizard's input handler. It is a small
// state machine: the current m.send.step decides which keys do what.
func (m Model) handleSendKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.send.recipientOpen && key == "esc" {
		m.send.recipientOpen = false
		m.send.recipientHScroll = 0
		m.send.address.Focus()
		return m, nil
	}
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.send.blurAll()
		return m, nil
	}
	if m.send.busy {
		return m, nil // ignore input while the final send RPC is in flight
	}
	if m.send.validating {
		return m, nil // do not race multiple validateaddress requests
	}
	switch m.send.step {
	case sendStepAddress:
		recipients := m.sendRecipients()
		if m.send.recipientOpen {
			switch key {
			case "left", "h":
				if m.send.recipientHScroll > 0 {
					m.send.recipientHScroll--
				}
			case "right", "l":
				if m.send.recipientHScroll < m.addrMaxScroll(recipients, 50) {
					m.send.recipientHScroll++
				}
			case "up", "k":
				if m.send.recipientCursor > 0 {
					m.send.recipientCursor--
				}
			case "down", "j":
				if m.send.recipientCursor < len(recipients)-1 {
					m.send.recipientCursor++
				}
			case "home", "g":
				m.send.recipientCursor = 0
			case "end", "G":
				if len(recipients) > 0 {
					m.send.recipientCursor = len(recipients) - 1
				}
			case "enter", "tab", "shift+tab":
				if m.send.recipientCursor < len(recipients) {
					m.send.address.SetValue(recipients[m.send.recipientCursor].Address)
				}
				m.send.recipientOpen = false
				m.send.address.Focus()
			}
			return m, nil
		}
		if (key == "tab" || key == "down") && len(recipients) > 0 {
			m.send.recipientOpen = true
			m.send.recipientHScroll = 0
			m.send.address.Blur()
			return m, nil
		}
		if key == "enter" {
			// Fire a validate RPC; the validateMsg handler advances the step.
			if v := m.send.address.Value(); v != "" {
				m.send.validating = true
				m.send.errMsg = ""
				return m, validateAddr(m.rpc, v)
			}
			return m, nil
		}
		// Any other key: hand it to the textinput so it can edit itself.
		// The textinput returns a new Model we have to assign back.
		var cmd tea.Cmd
		m.send.address, cmd = m.send.address.Update(msg)
		return m, cmd
	case sendStepAmount:
		if key == "enter" {
			amt, err := strconv.ParseFloat(m.send.amount.Value(), 64)
			if err != nil || amt <= 0 {
				m.send.errMsg = "enter a positive amount"
				return m, nil
			}
			if amt > m.wallet.Balance {
				avail := format.FormatGRCPlain(m.wallet.Balance)
				if m.anonymous {
					avail = format.MaskedAmount
				}
				m.send.errMsg = fmt.Sprintf("amount exceeds balance (%s available)", avail)
				return m, nil
			}
			m.send.errMsg = ""
			m.send.amountValue = amt
			m.send.amount.Blur()
			if m.send.needsUnlock {
				m.send.step = sendStepPassphrase
				m.send.passphrase.Focus()
			} else {
				m.send.step = sendStepConfirm
			}
			return m, nil
		}
		// backspace on an empty amount field walks us back to the address step.
		if key == "backspace" && m.send.amount.Value() == "" {
			m.send.step = sendStepAddress
			m.send.amount.Blur()
			m.send.address.Focus()
			return m, nil
		}
		var cmd tea.Cmd
		m.send.amount, cmd = m.send.amount.Update(msg)
		return m, cmd
	case sendStepPassphrase:
		if key == "enter" {
			if m.send.passphrase.Value() == "" {
				m.send.errMsg = "passphrase required"
				return m, nil
			}
			m.send.errMsg = ""
			m.send.passphrase.Blur()
			m.send.step = sendStepConfirm
			return m, nil
		}
		var cmd tea.Cmd
		m.send.passphrase, cmd = m.send.passphrase.Update(msg)
		return m, cmd
	case sendStepConfirm:
		if key == "y" || key == "enter" {
			m.send.busy = true
			return m, runSend(m.rpc, m.send.address.Value(), m.send.amountValue,
				m.send.passphrase.Value(), m.send.needsUnlock)
		}
		if key == "n" {
			m.mode = modeDashboard
			return m, nil
		}
		return m, nil
	case sendStepResult:
		// Any key dismisses the result screen.
		m.mode = modeDashboard
		return m, nil
	}
	return m, nil
}

// ---- Sign-message modal -----------------------------------------------

func (m Model) renderSendModal() string {
	var body string
	switch m.send.step {
	case sendStepAddress:
		body = "Recipient address:\n\n" + m.send.address.View()
		recipients := m.sendRecipients()
		if m.send.recipientOpen {
			// Match the address panel: one clipped row per entry, with the
			// same horizontal pan keys rather than wrapping long labels or
			// addresses onto multiple lines.
			const rowWidth = 50
			maxScroll := m.addrMaxScroll(recipients, rowWidth)
			hoff := min(m.send.recipientHScroll, maxScroll)
			header := "Saved addresses:"
			if maxScroll > 0 {
				header += "  ←/→"
			}
			body += "\n\n" + theme.Label.Render(header)
			const maxRows = 6
			start := 0
			if m.send.recipientCursor >= maxRows {
				start = m.send.recipientCursor - maxRows + 1
			}
			end := min(start+maxRows, len(recipients))
			for i := start; i < end; i++ {
				a := recipients[i]
				prefix := "  "
				row := ui.ClipSegments(addressRowSegments(a, m.anonymous, m.ownership(a.Address)), hoff, rowWidth)
				if i == m.send.recipientCursor {
					prefix = theme.Accent.Render("▸ ")
					row = ui.FillBackground(row, rowWidth)
				}
				body += "\n" + prefix + row
			}
			body += "\n\n" + theme.Muted.Render("↑/↓ select · ←/→ pan · enter to use · esc to close")
			break
		}
		if m.send.validating {
			body += "\n\n" + theme.Muted.Render("validating…")
		} else if m.send.errMsg != "" {
			// errMsg carries the validateaddress RPC error verbatim on this
			// step (and unlock/send errors elsewhere), so it's daemon text
			// like any other, sanitized at every render below.
			body += "\n\n" + theme.Bad.Render(format.SanitizeTerminal(m.send.errMsg))
		} else {
			if len(recipients) > 0 {
				body += "\n\n" + theme.Muted.Render("enter to validate · tab or ↓ for saved addresses · esc to cancel")
			} else {
				body += "\n\n" + theme.Muted.Render("enter to validate · esc to cancel")
			}
		}
	case sendStepAmount:
		body = "Amount (GRC):\n\n" + m.send.amount.View()
		avail := format.FormatGRCPlain(m.wallet.Balance)
		if m.anonymous {
			avail = format.MaskedAmount
		}
		body += "\n\n" + theme.Muted.Render("available: "+avail)
		if m.send.errMsg != "" {
			body += "\n\n" + theme.Bad.Render(format.SanitizeTerminal(m.send.errMsg))
		} else {
			body += "\n\n" + theme.Muted.Render("enter to continue · backspace to go back · esc to cancel")
		}
	case sendStepPassphrase:
		body = "Wallet is locked. Passphrase:\n\n" + m.send.passphrase.View()
		if m.send.errMsg != "" {
			body += "\n\n" + theme.Bad.Render(format.SanitizeTerminal(m.send.errMsg))
		} else {
			body += "\n\n" + theme.Muted.Render("enter to continue · esc to cancel")
		}
	case sendStepConfirm:
		confirmAmount := format.FormatGRCFullPlain(m.send.amountValue)
		if m.anonymous {
			confirmAmount = format.MaskedAmount
		}
		body = theme.Title.Render("Confirm send") + "\n\n"
		body += fmt.Sprintf("  To:     %s\n", m.send.address.Value())
		body += fmt.Sprintf("  Amount: %s\n", confirmAmount)
		body += "\n" + theme.Muted.Render("[y] broadcast   [n] cancel")
	case sendStepResult:
		if m.send.resultErr != "" {
			body = theme.Bad.Render("send failed") + "\n\n" + format.SanitizeTerminal(m.send.resultErr)
		} else {
			// The txid is the daemon's response string too, sanitized like
			// the error branch above.
			body = theme.Good.Render("sent ✓") + "\n\n" + "txid: " + format.SanitizeTerminal(m.send.resultTxID)
		}
		body += "\n\n" + theme.Muted.Render("press any key to close")
	}

	modal := ui.ModalBox(60, "Send GRC", body)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// onValidateMsg handles validateMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onValidateMsg(msg validateMsg) (tea.Model, tea.Cmd) {
	m.send.validating = false
	if msg.err != nil {
		m.send.errMsg = msg.err.Error()
		return m, nil
	}
	if !msg.v.IsValid {
		m.send.errMsg = "address is not valid"
		return m, nil
	}
	m.send.errMsg = ""
	m.send.step = sendStepAmount
	m.send.address.Blur()
	m.send.amount.Focus()
	return m, nil
}

// onSendResultMsg handles sendResultMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onSendResultMsg(msg sendResultMsg) (tea.Model, tea.Cmd) {
	m.send.busy = false
	m.send.step = sendStepResult
	if msg.err != nil {
		m.send.resultErr = msg.err.Error()
	} else {
		m.send.resultTxID = msg.txid
	}
	// Refresh the tx list so the just-broadcast transaction appears.
	spin := m.bumpInflight(1)
	return m, tea.Batch(fetchTxs(m.rpc, m.txsLastBlock), spin)
}

// sendStep is the state-machine step inside the send modal. The wizard
// walks the user through address → amount → (passphrase) → confirm →
// result.
type sendStep int

const (
	sendStepAddress    sendStep = iota // type or choose + validate the recipient
	sendStepAmount                     // type the amount
	sendStepPassphrase                 // only used when the wallet is encrypted + locked
	sendStepConfirm                    // show "are you sure?"
	sendStepResult                     // show txid or error
)

// sendState is the live state of the send modal. amountValue is cached
// once the user leaves the amount step so the confirm view doesn't have
// to re-parse the string.
type sendState struct {
	step sendStep
	// recipientOpen controls the saved-recipient dropdown. recipientCursor
	// indexes Model.sendRecipients(), which contains only labeled entries.
	recipientOpen    bool
	recipientCursor  int
	recipientHScroll int
	address          textinput.Model
	amount           textinput.Model
	passphrase       textinput.Model
	amountValue      float64 // parsed once when leaving the amount step
	needsUnlock      bool    // true if the daemon says the wallet is currently locked
	validating       bool    // true while an address validateaddress RPC is in flight
	errMsg           string  // per-step validation / RPC error message
	busy             bool    // true while the final send command is running
	resultTxID       string  // populated in sendStepResult on success
	resultErr        string  // populated in sendStepResult on failure
}

// blurAll takes focus away from every input in the send state. Called when
// closing the modal so the cursor doesn't blink on an invisible field.
// Pointer receiver (*sendState) so mutations to the embedded textinputs
// actually stick.
func (s *sendState) blurAll() {
	s.address.Blur()
	s.amount.Blur()
	s.passphrase.Blur()
}

// sendRecipients returns labeled address-book entries for the send wizard.
// Unlabeled wallet addresses are deliberately omitted: the picker is for the
// destinations a user has saved a name for, while the manual option accepts
// every valid Gridcoin address.
func (m Model) sendRecipients() []rpc.ReceivedAddress {
	var out []rpc.ReceivedAddress
	for _, a := range m.addresses {
		if a.DisplayLabel() != "" {
			out = append(out, a)
		}
	}
	return out
}
