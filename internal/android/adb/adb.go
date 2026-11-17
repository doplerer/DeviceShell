// Package adb speaks the adb server's smart-socket protocol directly, so the frequent
// commands skip starting adb.exe (tens of milliseconds each on Windows, per device and
// step). A request the server cannot open before the device runs anything (server not
// started yet, adbd without shell v2, ...) fails with ErrUnserved, and the caller can
// still run it through adb.exe, which also starts the server.
package adb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrUnserved means nothing reached the device: adb.exe may safely run the command instead.
var ErrUnserved = errors.New("adb server did not serve the request")

// serverAddr follows adb.exe: ANDROID_ADB_SERVER_PORT or 5037 on loopback.
func serverAddr() string {
	port := os.Getenv("ANDROID_ADB_SERVER_PORT")
	if _, e := strconv.Atoi(port); e != nil {
		port = "5037"
	}
	return net.JoinHostPort("127.0.0.1", port)
}

type connection struct {
	net.Conn
	stop func() bool
}

func (a *connection) Close() error {
	a.stop()
	return a.Conn.Close()
}

// dial connects to the server; cancelling c ends any blocked read or write.
func dial(c context.Context) (*connection, error) {
	var d net.Dialer
	conn, e := d.DialContext(c, "tcp", serverAddr())
	if e != nil {
		return nil, e
	}
	return &connection{conn, context.AfterFunc(c, func() { conn.SetDeadline(time.Unix(1, 0)) })}, nil
}

// request sends one length-prefixed service request and reads its OKAY/FAIL status.
func (a *connection) request(service string) error {
	if _, e := fmt.Fprintf(a, "%04x%s", len(service), service); e != nil {
		return e
	}
	return a.status()
}
func (a *connection) status() error {
	var s [4]byte
	if _, e := io.ReadFull(a, s[:]); e != nil {
		return e
	}
	switch string(s[:]) {
	case "OKAY":
		return nil
	case "FAIL":
		msg, e := a.protocolString()
		if e != nil {
			return e
		}
		return errors.New(msg)
	}
	return fmt.Errorf("unexpected adb status %q", s)
}

// protocolString reads a payload prefixed by its length in four hex digits.
func (a *connection) protocolString() (string, error) {
	var n [4]byte
	if _, e := io.ReadFull(a, n[:]); e != nil {
		return "", e
	}
	size, e := strconv.ParseUint(string(n[:]), 16, 32)
	if e != nil {
		return "", e
	}
	b := make([]byte, size)
	_, e = io.ReadFull(a, b)
	return string(b), e
}

// open connects to serial (or only to the server when serial is empty) and opens
// service. Any failure is ErrUnserved: the device has not run anything yet.
func open(c context.Context, serial, service string) (*connection, error) {
	a, e := dial(c)
	if e != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnserved, e)
	}
	if serial != "" {
		e = a.request("host:transport:" + serial)
	}
	if e == nil {
		e = a.request(service)
	}
	if e != nil {
		a.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnserved, e)
	}
	return a, nil
}

// HostQuery runs a server-side request. Listings answer with one payload; forward
// requests answer with a second status and, for tcp:0, the port they chose.
func HostQuery(c context.Context, service string, listing bool) (string, error) {
	a, e := open(c, "", service)
	if e != nil {
		return "", e
	}
	defer a.Close()
	if listing {
		return a.protocolString()
	}
	if e = a.status(); e != nil {
		return "", fmt.Errorf("%w: %v", ErrUnserved, e)
	}
	out, e := a.protocolString()
	if errors.Is(e, io.EOF) {
		return "", nil
	}
	return out, e
}

// Shell protocol v2 packets: id byte, little-endian uint32 length, data.
const (
	shellStdout     = 1
	shellStderr     = 2
	shellExit       = 3
	shellCloseStdin = 4
)

// Shell runs cmd like "adb -s serial shell cmd": stdout and stderr interleaved as they
// arrive, and the remote exit status as the error, as adb.exe reports it.
func Shell(c context.Context, serial, cmd string) (string, error) {
	a, e := open(c, serial, "shell,v2,raw:"+cmd)
	if e != nil {
		return "", e
	}
	defer a.Close()
	// adb.exe closes the remote stdin when its own is not a terminal; commands that read it end.
	if _, e = a.Write([]byte{shellCloseStdin, 0, 0, 0, 0}); e != nil {
		return "", e
	}
	var out strings.Builder
	var head [5]byte
	for {
		if _, e = io.ReadFull(a, head[:]); e != nil {
			if c.Err() != nil {
				e = c.Err()
			}
			return out.String(), e
		}
		data := make([]byte, binary.LittleEndian.Uint32(head[1:]))
		if _, e = io.ReadFull(a, data); e != nil {
			return out.String(), e
		}
		switch head[0] {
		case shellStdout, shellStderr:
			out.Write(data)
		case shellExit:
			if len(data) == 1 && data[0] != 0 {
				return out.String(), fmt.Errorf("exit status %d", data[0])
			}
			return out.String(), nil
		}
	}
}

// ExecOut streams the raw output of cmd, like "adb -s serial exec-out cmd". ok is false
// when the command could not start natively; nothing has been written to w then.
func ExecOut(c context.Context, serial, cmd string, w io.Writer) (ok bool, err error) {
	a, e := open(c, serial, "exec:"+cmd)
	if e != nil {
		return c.Err() != nil, e
	}
	defer a.Close()
	if _, e = io.Copy(w, a); e != nil && c.Err() != nil {
		e = c.Err()
	}
	return true, e
}
