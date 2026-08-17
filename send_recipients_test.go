package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSendRecipientPickerUsesLabeledAddresses(t *testing.T) {
	m := NewModel(Config{}, nil)
	m.addresses = []ReceivedAddress{
		{Address: "SUnlabeled"},
		{Address: "SSaved", Account: "Alice"},
		{Address: "SModern", Label: "Bob"},
	}
	m.openSendModal()

	if m.send.step != sendStepAddress || m.send.recipientOpen || !m.send.address.Focused() {
		t.Fatalf("send form should start with its manual address input focused: %+v", m.send)
	}
	if got := m.sendRecipients(); len(got) != 2 || got[0].Address != "SSaved" || got[1].Address != "SModern" {
		t.Fatalf("send recipients = %+v, want the two labeled addresses", got)
	}

	next, _ := m.handleSendKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if !m.send.recipientOpen || m.send.recipientCursor != 0 {
		t.Fatalf("dropdown = %+v, want first saved recipient selected", m.send)
	}
	next, cmd := m.handleSendKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.send.recipientOpen || m.send.address.Value() != "SSaved" || !m.send.address.Focused() {
		t.Fatalf("saved recipient selection = %+v, cmd=%v", m.send, cmd)
	}
}

func TestSendRecipientPickerAllowsManualAddress(t *testing.T) {
	m := NewModel(Config{}, nil)
	m.openSendModal()

	next, _ := m.handleSendKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("SManual")})
	m = next.(Model)
	if m.send.address.Value() != "SManual" || !m.send.address.Focused() {
		t.Fatalf("manual entry = %+v", m.send)
	}
}

func TestSendRecipientDropdownPansLongRows(t *testing.T) {
	m := NewModel(Config{}, nil)
	m.addresses = []ReceivedAddress{{
		Address: "S123456789012345678901234567890123456789012345678901234567890123",
		Account: "A very long saved recipient label",
	}}
	m.openSendModal()

	next, _ := m.handleSendKey(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	next, _ = m.handleSendKey(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(Model)
	if m.send.recipientHScroll != 1 {
		t.Fatalf("horizontal scroll = %d, want 1", m.send.recipientHScroll)
	}
}

func TestTransactionRowShowsSavedRecipientLabel(t *testing.T) {
	tx := Transaction{Category: "send", Address: "SRecipientAddress123456789", Amount: -1, Confirmations: 10}
	out := renderTxRowLabeled(tx, false, "", "stamp.gridcoin.club")
	if !strings.Contains(out, "stamp.gridcoin.cl…") {
		t.Fatalf("transaction row should include the shortened saved label, got:\n%s", out)
	}
}
