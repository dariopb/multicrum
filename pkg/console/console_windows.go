//go:build windows

package console

import (
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinConsole is a Windows ConPTY-backed terminal session.
type WinConsole struct {
	handle  windows.Handle
	inPipe  *os.File // we write keystrokes here
	outPipe *os.File // we read output here
	process *os.Process
	done    chan struct{}
}

// NewWinConsole launches cmdStr inside a ConPTY of the given size.
func NewWinConsole(cmdStr string, cols, rows int) (*WinConsole, error) {
	// Pipe layout (mirrors go-pty exactly):
	//   ptyIn    – ConPTY reads from here  (input path)  → we own the write end (inPipeOurs)
	//   ptyOut   – ConPTY writes here      (output path) → we own the read end  (outPipeOurs)
	ptyIn, inPipeOurs, err := os.Pipe() // ptyIn=read, inPipeOurs=write
	if err != nil {
		return nil, fmt.Errorf("os.Pipe (input): %w", err)
	}
	outPipeOurs, ptyOut, err := os.Pipe() // outPipeOurs=read, ptyOut=write
	if err != nil {
		ptyIn.Close()
		inPipeOurs.Close()
		return nil, fmt.Errorf("os.Pipe (output): %w", err)
	}

	coord := windows.Coord{X: int16(cols), Y: int16(rows)}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(coord,
		windows.Handle(ptyIn.Fd()),
		windows.Handle(ptyOut.Fd()),
		0, &hpc); err != nil {
		ptyIn.Close()
		inPipeOurs.Close()
		outPipeOurs.Close()
		ptyOut.Close()
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}
	// ConPTY has duplicated the handles; close our originals.
	ptyIn.Close()
	ptyOut.Close()

	// Build attribute list.
	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		inPipeOurs.Close()
		outPipeOurs.Close()
		return nil, fmt.Errorf("NewProcThreadAttributeList: %w", err)
	}
	// Pass the HPCON value (not a pointer to it) as lpValue.
	if err := attrList.Update(
		0x00020016, // PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE
		unsafe.Pointer(hpc),
		unsafe.Sizeof(hpc),
	); err != nil {
		attrList.Delete()
		windows.ClosePseudoConsole(hpc)
		inPipeOurs.Close()
		outPipeOurs.Close()
		return nil, fmt.Errorf("ProcThreadAttributeList.Update: %w", err)
	}

	// Build STARTUPINFOEX: set ProcThreadAttributeList, then pass &siEx.StartupInfo
	// to CreateProcess. Because StartupInfo is embedded first, the pointer value is
	// identical to *StartupInfoEx and Windows reads the extended field when
	// EXTENDED_STARTUPINFO_PRESENT is set.
	siEx := &windows.StartupInfoEx{}
	siEx.ProcThreadAttributeList = attrList.List()
	siEx.Cb = uint32(unsafe.Sizeof(*siEx))

	cmdlinePtr, err := windows.UTF16PtrFromString(cmdStr)
	if err != nil {
		attrList.Delete()
		windows.ClosePseudoConsole(hpc)
		inPipeOurs.Close()
		outPipeOurs.Close()
		return nil, err
	}

	var pi windows.ProcessInformation
	err = windows.CreateProcess(
		nil,
		cmdlinePtr,
		nil, nil,
		false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		nil, nil,
		&siEx.StartupInfo,
		&pi,
	)
	attrList.Delete() // safe to delete after CreateProcess returns
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		inPipeOurs.Close()
		outPipeOurs.Close()
		return nil, fmt.Errorf("CreateProcess: %w", err)
	}
	windows.CloseHandle(pi.Thread)

	proc, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.ClosePseudoConsole(hpc)
		inPipeOurs.Close()
		outPipeOurs.Close()
		return nil, fmt.Errorf("os.FindProcess: %w", err)
	}

	wc := &WinConsole{
		handle:  hpc,
		inPipe:  inPipeOurs,
		outPipe: outPipeOurs,
		process: proc,
		done:    make(chan struct{}),
	}
	go func() {
		_, _ = proc.Wait()
		close(wc.done)
	}()
	return wc, nil
}

func (wc *WinConsole) Read(p []byte) (int, error)  { return wc.outPipe.Read(p) }
func (wc *WinConsole) Write(p []byte) (int, error) { return wc.inPipe.Write(p) }

func (wc *WinConsole) Close() error {
	_ = wc.process.Kill()
	windows.ClosePseudoConsole(wc.handle)
	_ = wc.inPipe.Close()
	_ = wc.outPipe.Close()
	return nil
}

func (wc *WinConsole) Resize(cols, rows int) error {
	return windows.ResizePseudoConsole(wc.handle, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (wc *WinConsole) Done() <-chan struct{} { return wc.done }

var _ io.ReadWriteCloser = (*WinConsole)(nil)
