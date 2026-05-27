package ui

import "charm.land/lipgloss/v2"

var (
	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")).
				Padding(0, 1)

	tabExitedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Padding(0, 1)

	tabBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("199"))

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)

	statusKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("62")).
			Background(lipgloss.Color("236")).
			Bold(true)

	viewportStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("0"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	helpModalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("235")).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2)

	exitChoiceActiveStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("62")).
				Padding(0, 1)

	exitChoiceInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")).
				Background(lipgloss.Color("235")).
				Padding(0, 1)

	// modalGapStyle paints literal spacing between styled segments
	// inside modal dialogs with the modal's background, so internal
	// ANSI resets from button styles don't leave the terminal-default
	// background visible (which looks like a black strip).
	modalGapStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235"))

	// selectorActiveStyle highlights the cursor row inside the
	// session selector modal.
	selectorActiveStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("62"))

	// selectorMovingStyle marks the row currently being moved (Space
	// reorder mode) so the user can see what's being dragged.
	selectorMovingStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("214"))

	// scrollIndicatorStyle paints the "current/total" indicator shown
	// in the top-right of the pane while scrolled into the scrollback
	// buffer. Bright white on the same hot-pink as the tab bar.
	scrollIndicatorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("199")).
				Padding(0, 1)
)
