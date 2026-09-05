package session

import (
	"encoding/base64"
	"strings"
)

const maxOSC52Payload = 1024 * 1024

// osc52Decoder extracts clipboard writes from the raw PTY stream. Terminal
// escape sequences may span reads, so decoding belongs beside the read loop
// rather than in the UI's per-message handler.
type osc52Decoder struct {
	state   byte
	payload []byte
}

func (d *osc52Decoder) Write(p []byte) []string {
	var texts []string
	for _, b := range p {
		switch d.state {
		case 0:
			if b == 0x1b {
				d.state = 1
			}
		case 1:
			if b == ']' {
				d.state = 2
			} else {
				d.restart(b)
			}
		case 2:
			if b == '5' {
				d.state = 3
			} else {
				d.restart(b)
			}
		case 3:
			if b == '2' {
				d.state = 4
			} else {
				d.restart(b)
			}
		case 4:
			if b == ';' {
				d.state = 5
				d.payload = d.payload[:0]
			} else {
				d.restart(b)
			}
		case 5:
			switch b {
			case 0x07:
				if text, ok := decodeOSC52Payload(d.payload); ok {
					texts = append(texts, text)
				}
				d.reset()
			case 0x1b:
				d.state = 6
			default:
				if len(d.payload) >= maxOSC52Payload {
					d.reset()
				} else {
					d.payload = append(d.payload, b)
				}
			}
		case 6:
			if b == '\\' {
				if text, ok := decodeOSC52Payload(d.payload); ok {
					texts = append(texts, text)
				}
				d.reset()
			} else {
				if len(d.payload)+2 > maxOSC52Payload {
					d.reset()
				} else {
					d.payload = append(d.payload, 0x1b, b)
					d.state = 5
				}
			}
		}
	}
	return texts
}

func (d *osc52Decoder) restart(b byte) {
	d.state = 0
	if b == 0x1b {
		d.state = 1
	}
}

func (d *osc52Decoder) reset() {
	d.state = 0
	d.payload = d.payload[:0]
}

func decodeOSC52Payload(payload []byte) (string, bool) {
	_, encoded, ok := strings.Cut(string(payload), ";")
	if !ok || encoded == "" || encoded == "?" {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return "", false
	}
	return string(decoded), true
}
