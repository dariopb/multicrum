package localserver

import (
	"encoding/binary"
	"fmt"
	"io"
)

const MaxFrameSize = 1 << 20

const (
	FrameInput byte = iota
	FrameOutput
	FrameMeta
	FrameClientHello
	FrameServerHello
	FrameResize
	FrameControl
	FrameControlAck
)

func WriteFrame(w io.Writer, typ byte, body []byte) error {
	if len(body)+1 > MaxFrameSize {
		return fmt.Errorf("frame too large: %d", len(body)+1)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)+1))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{typ}); err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	_, err := w.Write(body)
	return err
}

func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MaxFrameSize {
		return 0, nil, fmt.Errorf("invalid frame size: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}
