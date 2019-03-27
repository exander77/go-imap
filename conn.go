package imap

import (
	"bufio"
	"io"
	"net"
)

// A connection state.
// See RFC 3501 section 3.
type ConnState int

const (
	// In the connecting state, the server has not yet sent a greeting and no
	// command can be issued.
	ConnectingState = 0

	// In the not authenticated state, the client MUST supply
	// authentication credentials before most commands will be
	// permitted.  This state is entered when a connection starts
	// unless the connection has been pre-authenticated.
	NotAuthenticatedState ConnState = 1 << 0

	// In the authenticated state, the client is authenticated and MUST
	// select a mailbox to access before commands that affect messages
	// will be permitted.  This state is entered when a
	// pre-authenticated connection starts, when acceptable
	// authentication credentials have been provided, after an error in
	// selecting a mailbox, or after a successful CLOSE command.
	AuthenticatedState = 1 << 1

	// In a selected state, a mailbox has been selected to access.
	// This state is entered when a mailbox has been successfully
	// selected.
	SelectedState = AuthenticatedState + 1<<2

	// In the logout state, the connection is being terminated. This
	// state can be entered as a result of a client request (via the
	// LOGOUT command) or by unilateral action on the part of either
	// the client or server.
	LogoutState = 1 << 3

	// ConnectedState is either NotAuthenticatedState, AuthenticatedState or
	// SelectedState.
	ConnectedState = NotAuthenticatedState | AuthenticatedState | SelectedState
)

// A function that upgrades a connection.
//
// This should only be used by libraries implementing an IMAP extension (e.g.
// COMPRESS).
type ConnUpgrader func(conn net.Conn) (net.Conn, error)

// WriterWithFields makes possible to pass fields to debug log
type WriterWithFields interface {
	// SetField add  or set a field
	SetField(key, value string)

	// Writer clears added or changed field
	Writer() io.Writer
}

type debugWithFields struct {
	io.Writer

	local, remote WriterWithFields
}

// NewDebugWithFields contains debug writers with optional fields
func NewDebugWithFields(local, remote WriterWithFields) io.Writer {
	var w io.Writer
	if local != nil {
		w = local.Writer()
	}
	return &debugWithFields{Writer: w, local: local, remote: remote}
}

func (df *debugWithFields) withField(key, value string) {
	if df.local != nil {
		df.local.SetField(key, value)
	}
	if df.remote != nil {
		df.remote.SetField(key, value)
	}
}

func (df *debugWithFields) localWriter() io.Writer {
	if df.local == nil {
		return nil
	}
	df.Writer = df.local.Writer()
	return df.Writer
}

func (df *debugWithFields) remoteWriter() io.Writer {
	if df.remote == nil {
		return nil
	}
	return df.remote.Writer()
}

type debugWriter struct {
	io.Writer

	local  io.Writer
	remote io.Writer
}

// NewDebugWriter creates a new io.Writer that will write local network activity
// to local and remote network activity to remote.
func NewDebugWriter(local, remote io.Writer) io.Writer {
	return &debugWriter{Writer: local, local: local, remote: remote}
}

type multiFlusher struct {
	flushers []flusher
}

func (mf *multiFlusher) Flush() error {
	for _, f := range mf.flushers {
		if err := f.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func newMultiFlusher(flushers ...flusher) flusher {
	return &multiFlusher{flushers}
}

// An IMAP connection.
type Conn struct {
	net.Conn
	*Reader
	*Writer

	br *bufio.Reader
	bw *bufio.Writer

	waits chan struct{}

	// Print all commands and responses to this io.Writer.
	debug io.Writer
}

// NewConn creates a new IMAP connection.
func NewConn(conn net.Conn, r *Reader, w *Writer) *Conn {
	c := &Conn{Conn: conn, Reader: r, Writer: w}

	c.init()
	return c
}

func (c *Conn) init() {
	r := io.Reader(c.Conn)
	w := io.Writer(c.Conn)

	if c.debug != nil {
		localDebug, remoteDebug := c.debug, c.debug
		if debug, ok := c.debug.(*debugWriter); ok {
			localDebug, remoteDebug = debug.local, debug.remote
		}

		if debug, ok := c.debug.(*debugWithFields); ok {
			if addr := c.LocalAddr(); addr != nil {
				debug.withField("loc", addr.String())
			}
			if addr := c.RemoteAddr(); addr != nil {
				debug.withField("rem", addr.String())
			}

			localDebug, remoteDebug = debug.localWriter(), debug.remoteWriter()
		}

		if localDebug != nil {
			w = io.MultiWriter(c.Conn, localDebug)
		}
		if remoteDebug != nil {
			r = io.TeeReader(c.Conn, remoteDebug)
		}
	}

	if c.br == nil {
		c.br = bufio.NewReader(r)
		c.Reader.reader = c.br
	} else {
		c.br.Reset(r)
	}

	if c.bw == nil {
		c.bw = bufio.NewWriter(w)
		c.Writer.Writer = c.bw
	} else {
		c.bw.Reset(w)
	}

	if f, ok := c.Conn.(flusher); ok {
		c.Writer.Writer = struct {
			io.Writer
			flusher
		}{
			c.bw,
			newMultiFlusher(c.bw, f),
		}
	}
}

// Write implements io.Writer.
func (c *Conn) Write(b []byte) (n int, err error) {
	return c.Writer.Write(b)
}

// Flush writes any buffered data to the underlying connection.
func (c *Conn) Flush() error {
	if err := c.Writer.Flush(); err != nil {
		return err
	}
	return nil
}

// Upgrade a connection, e.g. wrap an unencrypted connection with an encrypted
// tunnel.
func (c *Conn) Upgrade(upgrader ConnUpgrader) error {
	// Flush all buffered data
	if err := c.Flush(); err != nil {
		return err
	}

	// Block reads and writes during the upgrading process
	c.waits = make(chan struct{})
	defer close(c.waits)

	upgraded, err := upgrader(c.Conn)
	if err != nil {
		return err
	}

	c.Conn = upgraded
	c.init()
	return nil
}

// Wait waits for the connection to be ready for reads and writes.
func (c *Conn) Wait() {
	if c.waits != nil {
		<-c.waits
	}
}

// SetDebug defines an io.Writer to which all network activity will be logged.
// If nil is provided, network activity will not be logged.
func (c *Conn) SetDebug(w io.Writer) {
	c.debug = w
	c.init()
}
