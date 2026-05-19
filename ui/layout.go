package ui

// paneSize computes the viewport size given total terminal dimensions.
// It subtracts the tab bar (1 line) and status bar (1 line).
func paneSize(totalCols, totalRows int) (cols, rows int) {
	cols = totalCols
	rows = totalRows - 2 // tab bar + status bar
	if rows < 1 {
		rows = 1
	}
	return
}
