package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tunnel "github.com/dariopb/goreverselb/pkg"
	"multicrum/pkg/localserver"
)

type readyFunc func(context.Context) (tunnel.ClientStatus, error)

func (f readyFunc) WaitReady(ctx context.Context) (tunnel.ClientStatus, error) {
	return f(ctx)
}

type startupTestOutput struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	changed chan struct{}
}

func newStartupTestOutput() *startupTestOutput {
	return &startupTestOutput{changed: make(chan struct{}, 1)}
}

func (o *startupTestOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	n, err := o.buffer.Write(p)
	o.mu.Unlock()
	select {
	case o.changed <- struct{}{}:
	default:
	}
	return n, err
}

func (o *startupTestOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.String()
}

func awaitStartupOutput(t *testing.T, output *startupTestOutput, text string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !strings.Contains(output.String(), text) {
		select {
		case <-output.changed:
		case <-timer.C:
			t.Fatalf("missing %q in startup output:\n%s", text, output.String())
		}
	}
}

func TestWaitReverseLBUsesAcknowledgement(t *testing.T) {
	called, acknowledged := make(chan struct{}), make(chan struct{})
	result := make(chan localserver.LBStatus, 1)
	errs := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		status, err := waitReverseLBReady(ctx, readyFunc(func(ctx context.Context) (tunnel.ClientStatus, error) {
			close(called)
			select {
			case <-acknowledged:
				return tunnel.ClientStatus{FrontendPort: 8123, FrontendAddress: "lb.example:8123"}, nil
			case <-ctx.Done():
				return tunnel.ClientStatus{}, ctx.Err()
			}
		}))
		result <- status
		errs <- err
	}()
	<-called
	select {
	case <-result:
		t.Fatal("reported readiness before upstream acknowledgement")
	default:
	}
	close(acknowledged)
	status := <-result
	if err := <-errs; err != nil || !status.Ready || status.FrontendPort != 8123 || status.FrontendAddress != "lb.example:8123" {
		t.Fatalf("lost acknowledged frontend: %+v, %v", status, err)
	}
}

func TestWaitReverseLBCancellationAndBindings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitReverseLBReady(ctx, readyFunc(func(ctx context.Context) (tunnel.ClientStatus, error) {
		return tunnel.ClientStatus{}, ctx.Err()
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	status, err := waitReverseLBReady(context.Background(), readyFunc(func(context.Context) (tunnel.ClientStatus, error) {
		return tunnel.ClientStatus{PublicationMode: "bindings_only"}, nil
	}))
	if err != nil || !status.Ready || status.FrontendPort != 0 {
		t.Fatalf("bindings-only readiness lost: %+v, %v", status, err)
	}
	_, err = waitReverseLBReady(context.Background(), readyFunc(func(context.Context) (tunnel.ClientStatus, error) {
		return tunnel.ClientStatus{}, nil
	}))
	if err == nil {
		t.Fatal("accepted a dedicated frontend without a port")
	}
}

func TestLaunchingClientWaitsForOwnerLBStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner")
	owner, err := localserver.ListenWithSettings(path, "startup", nil, localserver.ServerSettings{ReverseLB: &localserver.LBStatus{}})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	output := newStartupTestOutput()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan *localserver.LBStatus, 1)
	errs := make(chan error, 1)
	go func() {
		status, err := waitOwnerLBReady(ctx, path, "startup", output)
		result <- status
		errs <- err
	}()
	awaitStartupOutput(t, output, "Waiting for reverse LB")
	select {
	case <-result:
		t.Fatal("client continued before registration")
	default:
	}
	owner.SetLBStatus(localserver.LBStatus{Ready: true, FrontendPort: 8123, FrontendAddress: "lb.example:8123"})
	status := <-result
	if err := <-errs; err != nil || status == nil || status.FrontendPort != 8123 {
		t.Fatalf("client did not receive assigned port: %+v, %v", status, err)
	}
}

func TestNonInteractiveStartupPreservesInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner")
	owner, err := localserver.ListenWithSettings(path, "startup", nil, localserver.ServerSettings{})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err := writer.Write([]byte("application input\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	var output bytes.Buffer
	if err := announceNewOwnerLB(context.Background(), path, "startup", input, &output); err != nil || output.Len() != 0 {
		t.Fatalf("non-LB startup changed: %v", err)
	}
	owner.SetLBStatus(localserver.LBStatus{Ready: true, FrontendPort: 8123, FrontendAddress: "lb.example:8123"})
	if err := announceNewOwnerLB(context.Background(), path, "startup", input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "port 8123") || strings.Contains(output.String(), "Press any key") {
		t.Fatalf("noninteractive announcement = %q", output.String())
	}
	data, err := io.ReadAll(input)
	if err != nil || string(data) != "application input\n" {
		t.Fatalf("startup consumed application input: %q, %v", data, err)
	}
	output.Reset()
	owner.SetLBStatus(localserver.LBStatus{Ready: true, PublicationMode: "bindings_only"})
	if err := announceNewOwnerLB(context.Background(), path, "startup", input, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "no dedicated frontend port") {
		t.Fatalf("bindings-only announcement = %q", output.String())
	}
}
