package localserver

import "io"

type FanoutWriter struct {
	Primary io.Writer
	Mirror  io.Writer
}

func (w FanoutWriter) Write(p []byte) (int, error) {
	if w.Primary != nil {
		if _, err := w.Primary.Write(p); err != nil {
			return 0, err
		}
	}
	if w.Mirror != nil {
		_, _ = w.Mirror.Write(p)
	}
	return len(p), nil
}

func (w FanoutWriter) Read(p []byte) (int, error) {
	if r, ok := w.Primary.(io.Reader); ok {
		return r.Read(p)
	}
	return 0, io.EOF
}

func (w FanoutWriter) Close() error {
	if c, ok := w.Primary.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (w FanoutWriter) Fd() uintptr {
	if f, ok := w.Primary.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return 0
}
