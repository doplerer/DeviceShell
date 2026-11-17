package adb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
)

// fakeServer answers each connection with serve, given the requests read so far.
func fakeServer(t *testing.T, serve func(conn net.Conn, read func() string)) {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	t.Setenv("ANDROID_ADB_SERVER_PORT", strconv.Itoa(l.Addr().(*net.TCPAddr).Port))
	go func() {
		for {
			conn, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				serve(conn, func() string {
					var n [4]byte
					if _, e := io.ReadFull(conn, n[:]); e != nil {
						return ""
					}
					size, _ := strconv.ParseUint(string(n[:]), 16, 32)
					b := make([]byte, size)
					io.ReadFull(conn, b)
					return string(b)
				})
			}()
		}
	}()
}

func payload(s string) string { return fmt.Sprintf("%04x%s", len(s), s) }

func shellPacket(id byte, data string) []byte {
	b := []byte{id, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[1:], uint32(len(data)))
	return append(b, data...)
}

func TestExecOutStreamsRawBytes(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n\x00\x01"
	fakeServer(t, func(conn net.Conn, read func() string) {
		read()
		io.WriteString(conn, "OKAY")
		if read() == "exec:screencap -p" {
			io.WriteString(conn, "OKAY"+png)
		}
	})
	var b bytes.Buffer
	if ok, e := ExecOut(context.Background(), "R58M123", "screencap -p", &b); !ok || e != nil || b.String() != png {
		t.Fatalf("exec-out = %q, %v, %v", b.String(), ok, e)
	}
}

func TestShellKeepsOutputAndExitStatus(t *testing.T) {
	fakeServer(t, func(conn net.Conn, read func() string) {
		read()
		io.WriteString(conn, "OKAY")
		read()
		io.WriteString(conn, "OKAY")
		var closeStdin [5]byte
		io.ReadFull(conn, closeStdin[:])
		conn.Write(shellPacket(shellStdout, "out "))
		conn.Write(shellPacket(shellStderr, "err"))
		conn.Write(shellPacket(shellExit, "\x02"))
	})
	out, e := Shell(context.Background(), "R58M123", "false")
	if out != "out err" || e == nil || e.Error() != "exit status 2" {
		t.Fatalf("shell = %q, %v", out, e)
	}
}

func TestUnservedWhenServerRefuses(t *testing.T) {
	fakeServer(t, func(conn net.Conn, read func() string) {
		read()
		io.WriteString(conn, "FAIL"+payload("device not found"))
	})
	if _, e := Shell(context.Background(), "NOPE", "id"); e == nil || !errors.Is(e, ErrUnserved) {
		t.Fatalf("want ErrUnserved, got %v", e)
	}
}
