package core

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
)

// fakeADBServer answers each connection with serve, given the requests read so far.
func fakeADBServer(t *testing.T, serve func(conn net.Conn, read func() string)) {
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

// Shell protocol v2 packet ids, as the adb server sends them.
const (
	shellStdout     = 1
	shellStderr     = 2
	shellExit       = 3
	shellCloseStdin = 4
)

func adbPayload(s string) string { return fmt.Sprintf("%04x%s", len(s), s) }

func shellPacket(id byte, data string) []byte {
	b := []byte{id, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(b[1:], uint32(len(data)))
	return append(b, data...)
}

func TestADBNativeListsDevices(t *testing.T) {
	fakeADBServer(t, func(conn net.Conn, read func() string) {
		if read() == "host:devices-l" {
			io.WriteString(conn, "OKAY"+adbPayload("R58M123\tdevice product:a model:SM_A515F transport_id:1\n"))
		}
	})
	app := New()
	app.Adb = "missing-adb.exe"
	d, e := androidBackend{app}.Discover(context.Background())
	if e != nil || len(d) != 1 || d[0].Serial != "R58M123" || d[0].Model != "SM_A515F" {
		t.Fatalf("devices = %+v, %v", d, e)
	}
}

func TestADBNativeShellKeepsOutputAndExitStatus(t *testing.T) {
	fakeADBServer(t, func(conn net.Conn, read func() string) {
		if read() != "host:transport:R58M123" {
			return
		}
		io.WriteString(conn, "OKAY")
		cmd := read()
		io.WriteString(conn, "OKAY")
		var closeStdin [5]byte
		if io.ReadFull(conn, closeStdin[:]); closeStdin[0] != shellCloseStdin {
			return
		}
		conn.Write(shellPacket(shellStdout, "out:"+strings.TrimPrefix(cmd, "shell,v2,raw:")+"\n"))
		conn.Write(shellPacket(shellStderr, "warn\n"))
		code := "\x00"
		if strings.Contains(cmd, "false") {
			code = "\x02"
		}
		conn.Write(shellPacket(shellExit, code))
	})
	app := New()
	app.Adb = "missing-adb.exe"
	out, e := app.Remote(context.Background(), "R58M123", "wm", "size")
	if e != nil || out != "out:'wm' 'size'\nwarn\n" {
		t.Fatalf("Remote = %q, %v", out, e)
	}
	out, e = app.Command(context.Background(), "-s", "R58M123", "shell", "false")
	if e == nil || e.Error() != "ADB: exit status 2: out:false\nwarn" || out != "out:false\nwarn\n" {
		t.Fatalf("failing shell = %q, %v", out, e)
	}
}

func TestADBNativeForwards(t *testing.T) {
	requests := make(chan string, 2)
	fakeADBServer(t, func(conn net.Conn, read func() string) {
		r := read()
		requests <- r
		if strings.Contains(r, ":forward:tcp:0;") {
			io.WriteString(conn, "OKAYOKAY"+adbPayload("27183"))
		} else {
			io.WriteString(conn, "OKAYOKAY")
		}
	})
	app := New()
	app.Adb = "missing-adb.exe"
	out, e := app.Command(context.Background(), "-s", "R58M123", "forward", "tcp:0", "localabstract:scrcpy_1a2b")
	if e != nil || out != "27183\n" || <-requests != "host-serial:R58M123:forward:tcp:0;localabstract:scrcpy_1a2b" {
		t.Fatalf("forward = %q, %v", out, e)
	}
	out, e = app.Command(context.Background(), "-s", "R58M123", "forward", "--remove", "tcp:27183")
	if e != nil || out != "" || <-requests != "host-serial:R58M123:killforward:tcp:27183" {
		t.Fatalf("remove forward = %q, %v", out, e)
	}
}

// A request the device never ran (here, adbd without shell v2) goes to adb.exe.
func TestADBNativeFallsBackWhenUnserved(t *testing.T) {
	fakeADBServer(t, func(conn net.Conn, read func() string) {
		read()
		io.WriteString(conn, "OKAY")
		read()
		io.WriteString(conn, "FAIL"+adbPayload("closed"))
	})
	app := New()
	if _, ok, _ := app.adbNative(context.Background(), []string{"-s", "R58M123", "shell", "id"}); ok {
		t.Fatal("an unserved shell must fall back to adb.exe")
	}
	if _, ok, _ := app.adbNative(context.Background(), []string{"-s", "R58M123", "install", "-r", "a.apk"}); ok {
		t.Fatal("install has no native path")
	}
	if _, ok, _ := app.adbNative(context.Background(), []string{"-s", "192.168.1.5:5555", "forward", "--remove", "tcp:1"}); ok {
		t.Fatal("serials with colons use adb.exe for host-serial requests")
	}
}

func TestADBNativeFallsBackWithoutServer(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("ANDROID_ADB_SERVER_PORT", strconv.Itoa(l.Addr().(*net.TCPAddr).Port))
	l.Close()
	if _, ok, _ := New().adbNative(context.Background(), []string{"devices", "-l"}); ok {
		t.Fatal("without a running server adb.exe must start it")
	}
}
