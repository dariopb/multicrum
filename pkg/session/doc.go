// Package session provides the core terminal session multiplexer primitives.
//
// A SessionManager owns a set of local or SSH-backed sessions. Each session is
// backed by something that behaves like an io.ReadWriteCloser: a Unix PTY, a
// Windows ConPTY, or ssh_client.RemoteSession. Output from children is read by
// the package and delivered to the embedding application through OutputMsg
// callbacks; input is sent by calling Session.Write with bytes from the host
// UI's terminal widget.
//
// The usual embedding pattern is:
//
//  1. Construct a manager with terminal dimensions and output/exit callbacks.
//  2. Start a local session with New, or a one-off local/SSH session with
//     NewWithSSH.
//  3. Render OutputMsg.Data in the parent UI keyed by OutputMsg.Index.
//  4. Forward user keystrokes for the focused pane with Session.Write.
//  5. Call ResizeAll or ResizeOne when the parent UI changes size.
//
// To bootstrap an application into a managed session, the parent process should
// start a new copy of the same executable in a child mode, for example
// []string{os.Args[0], "--child", "--mux-control", sockPath}. The child copy
// handles stdin/stdout/stderr normally inside the PTY. If it needs the parent to
// create more sessions, use an out-of-band control channel such as a Unix socket
// or named pipe; do not encode control messages into PTY stdout.
package session
