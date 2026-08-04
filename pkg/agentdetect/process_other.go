//go:build !linux

package agentdetect

type unavailableProcessInventory struct{}

func NewProcessInventory() ProcessInventory { return unavailableProcessInventory{} }

func (unavailableProcessInventory) Snapshot() ([]Process, error) {
	return nil, ErrProcessInventoryUnavailable
}
