package main

import "testing"

func TestAddLabelModalStartsBlankWithAddressFocused(t *testing.T) {
	m := NewModel(Config{}, nil)
	m.add.address.SetValue("old-address")
	m.add.label.SetValue("old-label")

	m.openAddLabelModal()
	if m.mode != modeAddLabel {
		t.Fatalf("mode = %v, want modeAddLabel", m.mode)
	}
	if m.add.address.Value() != "" || m.add.label.Value() != "" {
		t.Errorf("form = (%q, %q), want blank inputs", m.add.address.Value(), m.add.label.Value())
	}
	if m.add.focused != addLabelAddress || !m.add.address.Focused() {
		t.Errorf("address input should be focused, got focused=%v addressFocused=%v", m.add.focused, m.add.address.Focused())
	}
}

func TestAddLabelValidationGate(t *testing.T) {
	m := NewModel(Config{}, nil)
	m.openAddLabelModal()
	m.add.address.SetValue("SValidAddress")
	m.add.label.SetValue("recipient")
	m.add.validating = true

	next, cmd := m.Update(addLabelValidateMsg{v: ValidateAddress{IsValid: false}})
	got := next.(Model)
	if cmd != nil || got.add.validating || got.add.busy || got.add.errMsg != "address is not valid" {
		t.Errorf("invalid address state = %+v, command=%v", got.add, cmd)
	}

	m.add.validating = true
	next, cmd = m.Update(addLabelValidateMsg{v: ValidateAddress{IsValid: true}})
	got = next.(Model)
	if cmd == nil || got.add.validating || !got.add.busy || got.add.errMsg != "" {
		t.Errorf("valid address state = %+v, command=%v", got.add, cmd)
	}
}

func TestAddLabelResultClosesAndRefreshes(t *testing.T) {
	m := NewModel(Config{}, nil)
	m.openAddLabelModal()
	m.add.busy = true

	next, cmd := m.Update(addLabelResultMsg{})
	got := next.(Model)
	if cmd == nil || got.mode != modeDashboard || got.add.busy || got.add.address.Focused() || got.add.label.Focused() {
		t.Errorf("success state = %+v, mode=%v, command=%v", got.add, got.mode, cmd)
	}
}
