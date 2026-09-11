package session

import "github.com/charmbracelet/x/ansi"

// The upstream emulator accepts margins beyond the screen, which makes buffer
// edits (DL/IL, scrolling, etc.) panic. Resize replay can encounter margins
// recorded at a larger size; live output can race a resize in the same way.
// Consume invalid margin commands without changing the current scroll region.
// Returning false delegates valid commands to the emulator's default handler.
func (s *VTScreen) registerMarginGuards() {
	s.term.RegisterCsiHandler('r', func(params ansi.Params) bool {
		return marginsOutOfBounds(params, s.term.Height())
	})
	s.term.RegisterCsiHandler('s', func(params ansi.Params) bool {
		// Without DECLRMM, CSI s saves the cursor rather than setting margins.
		return s.leftRightMargin && marginsOutOfBounds(params, s.term.Width())
	})
}

func marginsOutOfBounds(params ansi.Params, size int) bool {
	start, _, _ := params.Param(0, 1)
	end, _, _ := params.Param(1, size)
	return start > size || end > size
}
