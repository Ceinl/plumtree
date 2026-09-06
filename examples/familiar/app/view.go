package main

import (
	"fmt"
	"strings"

	"github.com/Ceinl/plumtree/sdk/ui"
)

// vline is one rendered terminal row: an optional styled prefix plus a body.
type vline struct {
	prefix      string
	body        string
	prefixStyle ui.Style
	bodyStyle   ui.Style
}

// View renders the whole screen as one canvas: banner, transcript, notice,
// input, and footer. Text does not wrap in ui.Text, so familiar lays out and
// wraps its own lines here.
func (m *model) View() ui.Node {
	return ui.Canvas(max(24, m.w), max(10, m.h), m.draw)
}

func (m *model) draw(surface *ui.CanvasSurface) {
	w, h := surface.Width(), surface.Height()
	banner := m.bannerLines(w)
	noticeLines, noticeStyle := m.noticeLines(w)
	transcriptHeight := max(1, h-len(banner)-len(noticeLines)-2)

	lines := m.transcriptLines(w)
	y := 0
	for _, line := range banner {
		written := surface.SetText(1, y, line.prefix, line.prefixStyle)
		surface.SetText(1+written, y, line.body, line.bodyStyle)
		y++
	}
	start := max(0, len(lines)-transcriptHeight-max(0, m.scroll))
	for _, line := range lines[start:min(len(lines), start+transcriptHeight)] {
		written := surface.SetText(0, y, line.prefix, line.prefixStyle)
		surface.SetText(written, y, line.body, line.bodyStyle)
		y++
	}
	for _, line := range noticeLines {
		surface.SetText(0, y, line, noticeStyle)
		y++
	}
	written := surface.SetText(0, y, "› ", ui.Style{Role: ui.Accent})
	surface.SetText(written, y, string(m.input)+"▌", ui.Style{})
	y++
	surface.SetText(0, y, "enter ask · pgup/pgdn history · /help commands", ui.Style{Role: ui.Muted})
	if right := m.footerRight(w); right != "" {
		surface.SetText(max(0, w-runeLen(right)), y, right, ui.Style{Role: ui.Muted})
	}
}

// bannerLines is two or three rows: title, model + identity, resume hint.
func (m *model) bannerLines(w int) []vline {
	accent := ui.Style{Role: ui.Accent}
	muted := ui.Style{Role: ui.Muted}
	title := "✦ familiar — the model that lives in your terminal"
	if m.h < 16 {
		return []vline{{body: truncateRunes(title, max(8, w-2)), bodyStyle: accent}}
	}
	detail := m.cfg.Model
	switch {
	case !m.ready:
		detail += " · waking…"
	default:
		detail += " · you are " + m.label()
	}
	lines := []vline{
		{body: truncateRunes(title, max(8, w-2)), bodyStyle: accent},
		{body: truncateRunes(detail, max(8, w-2)), bodyStyle: muted},
	}
	if m.ready && m.turns() > 0 {
		resume := fmt.Sprintf("resumed %d turns · /new forgets", m.turns())
		lines = append(lines, vline{body: truncateRunes(resume, max(8, w-2)), bodyStyle: muted})
	}
	return lines
}

// transcriptLines renders the conversation, the thinking indicator, and the
// reveal cursor as styled rows.
func (m *model) transcriptLines(width int) []vline {
	plain := ui.Style{}
	muted := ui.Style{Role: ui.Muted}
	var lines []vline
	if !m.ready {
		return append(lines, vline{body: "waking — reading secrets and memory…", bodyStyle: muted})
	}
	if len(m.history) == 0 && m.phase == phaseIdle {
		lines = append(lines, vline{body: "say something — ask, think out loud, or type /help", bodyStyle: muted})
	}
	for index, msg := range m.history {
		content := msg.Content
		switch msg.Role {
		case "user":
			lines = appendV(lines, "you › ", content, ui.Style{Role: ui.Accent}, plain, width)
		default:
			if m.phase == phaseRevealing && index == len(m.history)-1 {
				content = string(m.revealText[:m.revealPos]) + "▌"
			}
			lines = appendV(lines, "familiar › ", content, ui.Style{Role: ui.Success}, plain, width)
		}
	}
	if m.phase == phaseThinking {
		lines = append(lines, vline{body: "familiar " + spinnerFrame(m.spinner) + " thinking…", bodyStyle: muted})
	}
	return lines
}

// noticeLines returns the wrapped transient status, capped so the input row
// always stays on screen.
func (m *model) noticeLines(w int) ([]string, ui.Style) {
	if m.notice.text == "" {
		return nil, ui.Style{}
	}
	style := ui.Style{Role: m.notice.role}
	lines := wrapText(m.notice.text, max(8, w-2))
	if len(lines) > 3 {
		lines = append(lines[:3], "…")
	}
	return lines, style
}

// footerRight renders the latest remote activity broadcast, right-aligned.
func (m *model) footerRight(w int) string {
	if m.presenceLeft <= 0 || m.presence.From == "" {
		return ""
	}
	label := m.presence.Name
	if label == "" {
		label = truncateRunes(m.presence.From, 8)
	}
	limit := max(12, w/2)
	return truncateRunes("↪ "+label+": "+m.presence.Text, limit)
}

// appendV appends a wrapped message; the prefix appears once and continuation
// lines are indented to match it.
func appendV(lines []vline, prefix, text string, prefixStyle, bodyStyle ui.Style, width int) []vline {
	bodyWidth := max(8, width-runeLen(prefix))
	for index, line := range wrapText(text, bodyWidth) {
		if index == 0 {
			lines = append(lines, vline{prefix: prefix, body: line, prefixStyle: prefixStyle, bodyStyle: bodyStyle})
			continue
		}
		lines = append(lines, vline{prefix: strings.Repeat(" ", runeLen(prefix)), body: line, bodyStyle: bodyStyle})
	}
	return lines
}

// wrapText greedily wraps on spaces, hard-breaking words that exceed width.
func wrapText(text string, width int) []string {
	if width < 4 {
		width = 4
	}
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		if strings.TrimSpace(paragraph) == "" {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range strings.Fields(paragraph) {
			for runeLen(word) > width {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, string([]rune(word)[:width]))
				word = string([]rune(word)[width:])
			}
			switch {
			case line == "":
				line = word
			case runeLen(line)+1+runeLen(word) <= width:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

func runeLen(text string) int { return len([]rune(text)) }
