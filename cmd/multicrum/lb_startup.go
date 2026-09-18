package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	tunnel "github.com/dariopb/goreverselb/pkg"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"
	"multicrum/pkg/localserver"
)

func waitReverseLBReady(ctx context.Context, client interface {
	WaitReady(context.Context) (tunnel.ClientStatus, error)
}) (localserver.LBStatus, error) {
	status, err := client.WaitReady(ctx)
	if err != nil {
		return localserver.LBStatus{}, fmt.Errorf("wait for reverse LB: %w", err)
	}
	if status.PublicationMode != "bindings_only" && (status.FrontendPort < 1 || status.FrontendPort > 65535) {
		return localserver.LBStatus{}, fmt.Errorf("reverse LB returned invalid frontend port %d", status.FrontendPort)
	}
	return localserver.LBStatus{
		Ready: true, FrontendPort: status.FrontendPort,
		FrontendAddress: status.FrontendAddress, PublicationMode: status.PublicationMode,
	}, nil
}

// The detached owner keeps running; only the terminal that launched it pauses.
func announceNewOwnerLB(ctx context.Context, path, server string, input *os.File, output io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	status, err := waitOwnerLBReady(ctx, path, server, output)
	if err != nil || status == nil {
		return err
	}
	if status.PublicationMode == "bindings_only" {
		_, err = fmt.Fprintln(output, "Reverse LB connected: bindings-only publication (no dedicated frontend port).")
	} else {
		_, err = fmt.Fprintf(output, "Reverse LB connected: frontend %q (port %d).\n", status.FrontendAddress, status.FrontendPort)
	}
	if err != nil {
		return err
	}
	return waitStartupKey(ctx, input, output)
}

func waitOwnerLBReady(ctx context.Context, path, server string, output io.Writer) (*localserver.LBStatus, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	announced := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status, err := localserver.ServerStatus(path, server)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			logPath, pathErr := localserver.LogPath(server)
			if pathErr != nil {
				return nil, errors.Join(err, pathErr)
			}
			return nil, fmt.Errorf("owner unavailable while waiting for reverse LB (see %s): %w", logPath, err)
		}
		lb := status.Settings.ReverseLB
		if lb == nil || lb.Ready {
			return lb, nil
		}
		if !announced {
			if _, err := fmt.Fprintln(output, "Waiting for reverse LB registration... (Ctrl+C cancels this attach)"); err != nil {
				return nil, err
			}
			announced = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitStartupKey(ctx context.Context, input *os.File, output io.Writer) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !term.IsTerminal(int(input.Fd())) {
		return nil
	}
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return fmt.Errorf("prepare startup keypress: %w", err)
	}
	defer func() { err = errors.Join(err, term.Restore(int(input.Fd()), state)) }()
	reader, err := cancelreader.NewReader(input)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	stop := context.AfterFunc(ctx, func() { reader.Cancel() })
	defer stop()
	if _, err := fmt.Fprint(output, "Press any key to attach (Ctrl+C cancels)... "); err != nil {
		return err
	}
	var key [4096]byte
	n, err := reader.Read(key[:])
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return io.EOF
	}
	if bytes.IndexByte(key[:n], 0x03) >= 0 {
		return context.Canceled
	}
	_, err = fmt.Fprint(output, "\r\n")
	return err
}
