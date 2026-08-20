// Package app provides the reusable multicrum owner lifecycle for applications
// that embed the TUI instead of invoking cmd/multicrum.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
	"multicrum/pkg/control"
	"multicrum/pkg/localserver"
	"multicrum/pkg/ui"
)

type Options struct {
	Server           string
	Command          []string
	ConnectionLayout string
	Terminal         *os.File
	Cols             int
	Rows             int
	Settings         localserver.ServerSettings
}

type Owner struct {
	once      sync.Once
	server    string
	socket    string
	program   *tea.Program
	model     *ui.Model
	local     *localserver.Owner
	control   *control.Service
	tokenPath string
	done      chan struct{}
}

// Start creates a complete in-process multicrum owner. Layout and terminal
// geometry are applied before the initial command starts, so embedded
// applications have the same startup behavior as a manually attached TUI.
func Start(ctx context.Context, options Options) (*Owner, error) {
	if options.Server == "" {
		options.Server = "default"
	}
	if len(options.Command) == 0 {
		options.Command = []string{defaultCommand()}
	}

	cols, rows := resolveSize(options)
	socketPath, err := localserver.SocketPath(options.Server)
	if err != nil {
		return nil, err
	}

	model := ui.NewModel(options.Command, cols, rows)
	model.SetServerName(options.Server)
	model.SetConnectionLayout(options.ConnectionLayout)
	input := ui.NewInputMux(nil)
	model.SetInputMux(input)

	settings := options.Settings
	if settings.Command == "" {
		settings.Command = options.Command[0]
	}
	local, err := localserver.ListenWithSettings(socketPath, options.Server, input, settings)
	if err != nil {
		return nil, err
	}
	model.SetClipboardOutput(local)
	model.SetClipboardHandler(local.WriteClipboard)
	model.SetDetachHandler(local.DetachActiveClient)

	program := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithColorProfile(colorprofile.TrueColor),
		tea.WithInput(input),
		tea.WithOutput(ui.NewKeyboardStripWriter(local)),
	)
	model.SetProgram(program)

	endpoint, err := control.Endpoint(options.Server)
	if err != nil {
		local.Close()
		model.CloseAgentDetection()
		return nil, err
	}
	token := control.NewToken()
	tokenPath := control.TokenPath(endpoint)
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		local.Close()
		model.CloseAgentDetection()
		return nil, err
	}
	service := control.NewService(options.Server, endpoint, token, model)
	model.SetControlService(service)
	if err := service.Start(); err != nil {
		os.Remove(tokenPath)
		local.Close()
		model.CloseAgentDetection()
		return nil, err
	}

	owner := &Owner{
		server: options.Server, socket: socketPath,
		program: program, model: model, local: local, control: service,
		tokenPath: tokenPath, done: make(chan struct{}),
	}
	local.SetCallbacks(func(n int) {
		program.Send(ui.LocalClientCountMsg(n))
	}, func(cols, rows int) {
		program.Send(tea.WindowSizeMsg{Width: cols, Height: rows})
	}, func(action string) {
		if action == "stop" {
			go owner.Close()
		}
	})
	go func() {
		_, _ = program.Run()
		close(owner.done)
		// Program termination must tear down the attach listener. Otherwise an
		// attached host waits forever for socket EOF and cannot reach its
		// deferred Owner.Close call.
		owner.Close()
	}()

	select {
	case <-model.Ready():
	case <-owner.done:
		owner.Close()
		return nil, fmt.Errorf("embedded owner stopped during initialization")
	case <-ctx.Done():
		owner.Close()
		return nil, ctx.Err()
	}
	program.Send(tea.WindowSizeMsg{Width: cols, Height: rows})
	syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := model.Sync(syncCtx); err != nil {
		owner.Close()
		return nil, fmt.Errorf("initialize embedded owner size: %w", err)
	}
	return owner, nil
}

func defaultCommand() string {
	if runtime.GOOS == "windows" {
		if command := os.Getenv("COMSPEC"); command != "" {
			return command
		}
		return "cmd.exe"
	}
	if command := os.Getenv("SHELL"); command != "" {
		return command
	}
	return "sh"
}

func resolveSize(options Options) (int, int) {
	cols, rows := options.Cols, options.Rows
	if (cols <= 0 || rows <= 0) && options.Terminal != nil {
		if width, height, err := term.GetSize(int(options.Terminal.Fd())); err == nil {
			if cols <= 0 {
				cols = width
			}
			if rows <= 0 {
				rows = height
			}
		}
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 36
	}
	return cols, rows
}

func (o *Owner) Model() *ui.Model      { return o.model }
func (o *Owner) Done() <-chan struct{} { return o.done }

// Attach connects a terminal through the normal multicrum attach protocol.
func (o *Owner) Attach(stdin *os.File, stdout io.Writer) error {
	attached, err := localserver.TryAttach(o.socket, o.server, stdin, stdout)
	if !attached && err == nil {
		return fmt.Errorf("multicrum server %q is not available", o.server)
	}
	return err
}

func (o *Owner) Close() {
	if o == nil {
		return
	}
	o.once.Do(func() {
		select {
		case <-o.done:
		default:
			o.program.Kill()
			<-o.done
		}
		_ = o.model.CloseSessions()
		_ = o.control.Close()
		_ = o.local.Close()
		o.model.CloseAgentDetection()
		_ = os.Remove(o.tokenPath)
	})
}
