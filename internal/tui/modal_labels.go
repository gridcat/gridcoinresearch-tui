package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

// runSetLabel fires the setaccount RPC and reports the outcome as a
// setLabelResultMsg. Unlike runSign/runSend there is no passphrase / unlock
// dance: setting a label is address-book metadata, not a signing operation.
func runSetLabel(c *rpc.Client, addr, label string) tea.Cmd {
	return func() tea.Msg {
		return setLabelResultMsg{err: c.SetAccount(addr, label)}
	}
}

func validateAddLabelAddress(c *rpc.Client, addr string) tea.Cmd {
	return func() tea.Msg {
		v, err := c.ValidateAddress(addr)
		return addLabelValidateMsg{v: v, err: err}
	}
}

func runAddLabel(c *rpc.Client, addr, label string) tea.Cmd {
	return func() tea.Msg {
		return addLabelResultMsg{err: c.SetAccount(addr, label)}
	}
}

// openEditLabelModal opens the edit-label modal pre-filled with the
// highlighted address's current label, so the user edits in place. It no-ops
// when there's no valid selection; the caller (handleKey "e") already gates on
// that, but reading through selectedAddress keeps the modal self-guarding.
func (m *Model) openEditLabelModal() {
	sel := m.selectedAddress()
	if sel == nil {
		return
	}
	m.mode = modeEditLabel
	// Reset the struct to clear any stale busy/errMsg from a previous open,
	// keeping the configured textinput (placeholder/width).
	m.edit = editLabelState{
		label:   m.edit.label,
		address: sel.Address,
	}
	m.edit.label.SetValue(sel.DisplayLabel())
	m.edit.label.CursorEnd()
	m.edit.label.Focus()
}

// handleEditLabelKey drives the single-input edit-label modal: esc cancels,
// enter submits the setaccount RPC (an empty value clears the label), input is
// ignored while the RPC is in flight, and every other key edits the textinput.
// There is no result phase: success closes the modal in the setLabelResultMsg
// handler, and an error returns here with the modal still open.
func (m Model) handleEditLabelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.edit.blurAll()
		return m, nil
	}
	if m.edit.busy {
		return m, nil // ignore input while setaccount is running
	}
	if key == "enter" {
		// Empty value is allowed; it clears the label.
		m.edit.errMsg = ""
		m.edit.busy = true
		return m, runSetLabel(m.rpc, m.edit.address, m.edit.label.Value())
	}
	var cmd tea.Cmd
	m.edit.label, cmd = m.edit.label.Update(msg)
	return m, cmd
}

// openAddLabelModal prepares a blank address-book form. The daemon accepts
// labels for external recipient addresses as well as wallet-owned ones, so
// this is intentionally available regardless of the focused dashboard panel.
func (m *Model) openAddLabelModal() {
	m.mode = modeAddLabel
	m.add = addLabelState{address: m.add.address, label: m.add.label}
	m.add.address.SetValue("")
	m.add.label.SetValue("")
	m.add.address.Focus()
}

func (m *Model) focusAddLabelField(field addLabelField) {
	m.add.blurAll()
	m.add.focused = field
	if field == addLabelAddress {
		m.add.address.Focus()
	} else {
		m.add.label.Focus()
	}
}

// handleAddLabelKey edits the two-field address-book form. Saving always
// validates the current address first, so a user can return to and change it
// after filling the label without bypassing validation.
func (m Model) handleAddLabelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.add.blurAll()
		return m, nil
	}
	if m.add.validating || m.add.busy {
		return m, nil
	}
	switch key {
	case "tab", "down":
		m.focusAddLabelField((m.add.focused + 1) % 2)
		return m, nil
	case "shift+tab", "up":
		m.focusAddLabelField((m.add.focused - 1 + 2) % 2)
		return m, nil
	case "enter":
		if m.add.focused == addLabelAddress {
			m.focusAddLabelField(addLabelName)
			return m, nil
		}
		if m.add.address.Value() == "" {
			m.add.errMsg = "address is required"
			m.focusAddLabelField(addLabelAddress)
			return m, nil
		}
		if m.add.label.Value() == "" {
			m.add.errMsg = "label is required"
			return m, nil
		}
		m.add.errMsg = ""
		m.add.validating = true
		return m, validateAddLabelAddress(m.rpc, m.add.address.Value())
	}
	var cmd tea.Cmd
	if m.add.focused == addLabelAddress {
		m.add.address, cmd = m.add.address.Update(msg)
	} else {
		m.add.label, cmd = m.add.label.Update(msg)
	}
	return m, cmd
}

// ---- Config modal -----------------------------------------------------

// renderEditLabelModal shows the address whose label is being edited (read
// only) plus the editable label input. Mirrors renderSignModal's double
// border centered layout, minus the multi-step machinery.
func (m Model) renderEditLabelModal() string {
	// Same daemon-sourced address string the panel shows (see
	// addressRowSegments), so it gets the same scrubbing on this surface.
	header := theme.Label.Render("Address: ") + theme.Accent.Render(format.SanitizeTerminal(m.edit.address))

	body := "Label:\n\n" + m.edit.label.View()
	// Heads-up for the setaccount quirk (see runSetLabel): relabeling an
	// address that is its account's current receiving address makes
	// gridcoinresearchd spawn a replacement carrying the old label. We can't
	// suppress it (setaccount is the only label RPC Gridcoin exposes), so we
	// warn rather than surprise. The modal's Width wraps this for us.
	body += "\n\n" + theme.Muted.Render("Heads-up: the daemon may spawn an extra address with the old label on save. harmless quirk, coins unaffected.")
	if m.edit.errMsg != "" {
		// Carries the setaccount RPC error verbatim on failure, so it gets
		// the same sanitizing as every other daemon-authored string.
		body += "\n\n" + theme.Bad.Render(format.SanitizeTerminal(m.edit.errMsg))
	} else {
		body += "\n\n" + theme.Muted.Render("enter to save · empty clears label · esc to cancel")
	}
	if m.edit.busy {
		body += "\n\n" + theme.Muted.Render("saving…")
	}

	modalWidth := 72
	if max := m.width - 2; modalWidth > max && max > 0 {
		modalWidth = max
	}

	modal := ui.ModalBox(modalWidth, "Edit label", header+"\n\n"+body)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) renderAddLabelModal() string {
	field := func(title string, active bool, value string) string {
		label := theme.Label.Render(title)
		if active {
			label = theme.Accent.Render("▸ " + title)
		}
		return label + "\n\n" + value
	}
	body := field("Address (could be any address, not just own):", m.add.focused == addLabelAddress, m.add.address.View()) + "\n\n" +
		field("Label:", m.add.focused == addLabelName, m.add.label.View())
	if m.add.validating {
		body += "\n\n" + theme.Muted.Render("validating address…")
	} else if m.add.busy {
		body += "\n\n" + theme.Muted.Render("saving…")
	} else if m.add.errMsg != "" {
		body += "\n\n" + theme.Bad.Render(format.SanitizeTerminal(m.add.errMsg))
	} else {
		body += "\n\n" + theme.Muted.Render("enter to save · tab to switch fields · esc to cancel")
	}

	modalWidth := 72
	if max := m.width - 2; modalWidth > max && max > 0 {
		modalWidth = max
	}
	modal := ui.ModalBox(modalWidth, "Add address label", body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// onSetLabelResultMsg handles setLabelResultMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onSetLabelResultMsg(msg setLabelResultMsg) (tea.Model, tea.Cmd) {
	m.edit.busy = false
	if msg.err != nil {
		// Keep the modal open so the user can read the error and retry.
		m.edit.errMsg = msg.err.Error()
		return m, nil
	}
	// Success: close the modal and refresh the address list so the new
	// label (Gridcoin's legacy "account") shows via DisplayLabel.
	m.edit.blurAll()
	m.mode = modeDashboard
	spin := m.bumpInflight(1)
	return m, tea.Batch(fetchAddrs(m.rpc), spin)
}

// onAddLabelValidateMsg handles addLabelValidateMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onAddLabelValidateMsg(msg addLabelValidateMsg) (tea.Model, tea.Cmd) {
	m.add.validating = false
	if msg.err != nil {
		m.add.errMsg = msg.err.Error()
		return m, nil
	}
	if !msg.v.IsValid {
		m.add.errMsg = "address is not valid"
		return m, nil
	}
	m.add.busy = true
	m.add.errMsg = ""
	return m, runAddLabel(m.rpc, m.add.address.Value(), m.add.label.Value())
}

// onAddLabelResultMsg handles addLabelResultMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onAddLabelResultMsg(msg addLabelResultMsg) (tea.Model, tea.Cmd) {
	m.add.busy = false
	if msg.err != nil {
		m.add.errMsg = msg.err.Error()
		return m, nil
	}
	m.add.blurAll()
	m.mode = modeDashboard
	spin := m.bumpInflight(1)
	return m, tea.Batch(fetchAddrs(m.rpc), spin)
}

// editLabelState is the live state of the edit-label modal. There is a single
// text input and no multi-step wizard (setting a label needs no passphrase),
// so it carries none of the step/needsUnlock/result machinery the send and
// sign modals do, only the input, the target address, an in-flight flag, and
// an error string. On success the modal closes outright, so there is no result
// field to populate.
type editLabelState struct {
	label   textinput.Model // editable label text (empty = clear the label)
	address string          // address whose label we're editing (read-only display)
	busy    bool            // true while the setaccount RPC is in flight
	errMsg  string          // RPC / validation error shown under the input
}

func (s *editLabelState) blurAll() {
	s.label.Blur()
}

// addLabelField identifies the active input in the add-address-label modal.
type addLabelField int

const (
	addLabelAddress addLabelField = iota
	addLabelName
)

// addLabelState is the two-field form for saving an arbitrary address-book
// entry. Unlike editLabelState, both inputs are editable because the address
// does not need to be present in the current wallet address list.
type addLabelState struct {
	focused    addLabelField
	address    textinput.Model
	label      textinput.Model
	validating bool
	busy       bool
	errMsg     string
}

func (s *addLabelState) blurAll() {
	s.address.Blur()
	s.label.Blur()
}
