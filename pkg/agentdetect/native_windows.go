//go:build windows

package agentdetect

type NativeServer struct{}

func ListenNativeUpdates(func(Update)) (*NativeServer, error) {
	return nil, ErrProcessInventoryUnavailable
}

func (s *NativeServer) Endpoint() string { return "" }
func (s *NativeServer) Close()           {}

func NativeTarget(id string, generation uint64) string { return "" }
