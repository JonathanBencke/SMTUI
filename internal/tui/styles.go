package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	accentColor = lipgloss.Color("#7DD3FC")
	dimColor    = lipgloss.Color("#6B7280")
	bgColor     = lipgloss.Color("#1E1E2E")
	borderColor = lipgloss.Color("#45475A")
	greenColor  = lipgloss.Color("#22C55E")
	yellowColor = lipgloss.Color("#EAB308")
	redColor    = lipgloss.Color("#EF4444")
	blueColor   = lipgloss.Color("#3B82F6")
	purpleColor = lipgloss.Color("#A855F7")
)

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(accentColor).
			Padding(0, 2)

	footerStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			Background(lipgloss.Color("#181825")).
			Padding(0, 1)

	profileBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1E1E2E")).
				Background(yellowColor).
				Padding(0, 1).
				Bold(true)

	serviceCardStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(borderColor).
				Padding(0, 1)

	serviceCardActive = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(accentColor).
				Padding(0, 1)

	logPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 1)

	logHeaderStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	statusRunning = lipgloss.NewStyle().
			Foreground(greenColor).Bold(true)
	statusBuilding = lipgloss.NewStyle().
			Foreground(yellowColor).Bold(true)
	statusGenerating = lipgloss.NewStyle().
				Foreground(purpleColor).Bold(true)
	statusIdle = lipgloss.NewStyle().
			Foreground(dimColor)
	statusCrashed = lipgloss.NewStyle().
			Foreground(redColor).Bold(true)
	statusStopping = lipgloss.NewStyle().
			Foreground(redColor).Bold(true)
	statusStopped = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9CA3AF"))

	serviceNameStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#CDD6F4"))

	keyHintStyle = lipgloss.NewStyle().
			Foreground(dimColor)

	keyActiveStyle = lipgloss.NewStyle().
			Foreground(accentColor).Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#313244")).
			Foreground(accentColor).
			Bold(true)

	successHint = lipgloss.NewStyle().
			Foreground(greenColor)

	dimLabelStyle = lipgloss.NewStyle().
			Foreground(dimColor)

	resStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94E2D5"))

	pidStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#89B4FA"))

	successHintStyle = lipgloss.NewStyle().
				Foreground(greenColor)

	// confirmMessageStyle destaca a pergunta do dialogo de confirmacao (x,
	// q/ctrl+c com servicos rodando) — mesma cor de alerta usada em "building".
	confirmMessageStyle = lipgloss.NewStyle().
				Foreground(yellowColor).
				Bold(true)

	// searchHighlightStyle destaca as ocorrencias da busca de logs (tecla "/")
	// dentro das linhas exibidas no painel.
	searchHighlightStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1E1E2E")).
				Background(yellowColor).
				Bold(true)

	tabActiveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1E1E2E")).
			Background(accentColor).
			Bold(true).
			Padding(0, 1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(dimColor).
				Padding(0, 1)

	tabSeparator = lipgloss.NewStyle().
			Foreground(borderColor).
			Render("│")
)

func statusStyle(s string) lipgloss.Style {
	switch s {
	case "running":
		return statusRunning
	case "building":
		return statusBuilding
	case "generating":
		return statusGenerating
	case "crashed":
		return statusCrashed
	case "stopping":
		return statusStopping
	case "stopped":
		return statusStopped
	default:
		return statusIdle
	}
}

func statusIcon(s string) string {
	switch s {
	case "running":
		return "●"
	case "building":
		return "◐"
	case "generating":
		return "◒"
	case "crashed":
		return "✖"
	case "stopping":
		return "◑"
	case "stopped":
		return "○"
	default:
		return "○"
	}
}
