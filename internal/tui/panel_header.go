package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
)

func fetchChain(c *rpc.Client) tea.Cmd {
	return func() tea.Msg {
		c, err := c.GetBlockchainInfo()
		return chainMsg{c, err}
	}
}

func fetchPeers(c *rpc.Client) tea.Cmd {
	return func() tea.Msg {
		p, err := c.GetPeerInfo()
		return peersMsg{p, err}
	}
}

// renderHeader draws the top bar: program name on the left, network badge
// in the middle, current block height right-aligned. We measure the two
// rendered halves with lipgloss.Width and pad the gap with spaces so the
// right half lands at the right edge of the box.
func (m Model) renderHeader() string {
	networkBadge := theme.MainnetBadge.Render("● mainnet")
	if m.cfg.Testnet {
		networkBadge = theme.TestnetBadge.Render("● testnet")
	}
	if m.chain.Chain == "test" && !m.cfg.Testnet {
		networkBadge = theme.Bad.Render("✗ daemon is TESTNET, TUI is mainnet")
	} else if m.chain.Chain == "main" && m.cfg.Testnet {
		networkBadge = theme.Bad.Render("✗ daemon is MAINNET, TUI is testnet")
	}

	title := theme.Title.Render("gridcoinresearch-tui")
	blockInfo := ""
	if m.chain.Blocks > 0 {
		blockInfo = theme.Muted.Render("block " + format.GroupThousandsInt64(m.chain.Blocks))
	}
	if m.peersLoaded {
		// Zero peers means the node is effectively offline, so render just
		// "peers 0" as a warning instead of a pointless (0↓/0↑) split.
		peers := theme.Warn.Render("peers 0")
		if m.peersTotal > 0 {
			peers = theme.Muted.Render(fmt.Sprintf("peers %d (%d↓/%d↑)",
				m.peersTotal, m.peersIn, m.peersOut))
		}
		if blockInfo != "" {
			blockInfo = blockInfo + theme.Muted.Render(" · ") + peers
		} else {
			blockInfo = peers
		}
	}

	// Right side always shows the running build version. When a newer release is
	// out, a compact green badge advertises the "u" key without duplicating the
	// latest version number shown in the update modal.
	rightParts := []string{}
	if m.updateAvailable && m.latestVersion != "" {
		rightParts = append(rightParts, theme.Good.Render("⬆"))
	}
	rightParts = append(rightParts, theme.Muted.Render(displayVersion()))
	if blockInfo != "" {
		rightParts = append(rightParts, blockInfo)
	}
	rightSide := strings.Join(rightParts, "  ")

	leftHalf := lipgloss.JoinHorizontal(lipgloss.Top, title, "  ", networkBadge)
	gap := m.width - lipgloss.Width(leftHalf) - lipgloss.Width(rightSide) - 4
	if gap < 1 {
		gap = 1
	}
	line := lipgloss.JoinHorizontal(lipgloss.Top,
		leftHalf,
		strings.Repeat(" ", gap),
		rightSide,
	)

	return theme.Border.Width(m.width - 2).Render(line)
}

// onChainMsg handles chainMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onChainMsg(msg chainMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	if msg.err != nil {
		m.walletErr = msg.err.Error()
	} else {
		m.chain = msg.c
	}
	return m, nil
}

// onPeersMsg handles peersMsg. Split out of Update so the whole of this
// message's handling lives beside the rest of its feature.
func (m Model) onPeersMsg(msg peersMsg) (tea.Model, tea.Cmd) {
	m.finishFetch()
	if msg.err != nil {
		m.walletErr = msg.err.Error()
	} else {
		m.peersTotal = len(msg.peers)
		m.peersIn = 0
		for _, p := range msg.peers {
			if p.Inbound {
				m.peersIn++
			}
		}
		m.peersOut = m.peersTotal - m.peersIn
		m.peersLoaded = true
		// Held only so a peer-sharing tick has something to report
		// without issuing an RPC of its own.
		m.peers = msg.peers
	}
	return m, nil
}
