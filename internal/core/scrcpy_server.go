package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// scrcpyServer is one run of the scrcpy server (the pinned 5.0.1 in the tool folder) on
// a device, reached through an ADB forward. The server accepts its sockets in order
// (video, then control) and answers the first one with a dummy byte.
type scrcpyServer struct {
	app                      *Service
	serial, remotePath, port string
	cmd                      *exec.Cmd
	cancel                   context.CancelFunc
	done                     chan struct{} // closed when the server exits
	log                      *DiagnosticTail
	once                     sync.Once
}

// scrcpyJar is where every server of a device runs from: one fixed file, pushed only
// when it is missing or differs, instead of one push (and delete) per server.
const scrcpyJar = "/data/local/tmp/deviceshell-scrcpy-5.0.1.jar"

// legacyScrcpyJar is the same server as pushed by versions released as BOTFARM; it is
// removed, best effort, when the current one is pushed.
const legacyScrcpyJar = "/data/local/tmp/botfarm-scrcpy-5.0.1.jar"

// scrcpyOptions makes the server leave its file in place (cleanup=false): others run
// from it. It adds no other option, as Samsung's Android 14 aborts with a dozen.
func scrcpyOptions(options string) string {
	fields := strings.Fields(options)
	for i, f := range fields {
		if strings.HasPrefix(f, "cleanup=") {
			fields[i] = "cleanup=false"
			return strings.Join(fields, " ")
		}
	}
	return strings.Join(append(fields, "cleanup=false"), " ")
}

// ensureScrcpyJar pushes the server to scrcpyJar unless the same size is already there.
// The push goes to a temporary name and is then renamed, so a server starting meanwhile
// never reads half a file.
func (app *Service) ensureScrcpyJar(c context.Context, serial string) error {
	local := filepath.Join(app.ToolDir, "scrcpy-server")
	st, e := os.Stat(local)
	if e != nil {
		return e
	}
	mu, _ := app.jarLocks.LoadOrStore(serial, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if out, e := app.Remote(c, serial, "stat", "-c", "%s", scrcpyJar); e == nil && strings.TrimSpace(out) == strconv.FormatInt(st.Size(), 10) {
		return nil
	}
	token := make([]byte, 4)
	rand.Read(token)
	temporary := scrcpyJar + "." + hex.EncodeToString(token)
	if _, e := app.Command(c, "-s", serial, "push", local, temporary); e != nil {
		return e
	}
	if _, e := app.Remote(c, serial, "mv", "-f", temporary, scrcpyJar); e != nil {
		app.Remote(c, serial, "rm", "-f", temporary)
		return e
	}
	app.Remote(c, serial, "rm", "-f", legacyScrcpyJar)
	return nil
}

// startScrcpyServer pushes the server if needed and starts it with options ("key=value ...").
func (app *Service) startScrcpyServer(parent context.Context, serial, options string) (*scrcpyServer, error) {
	c, cancel := context.WithCancel(parent)
	token := make([]byte, 4)
	rand.Read(token)
	token[0] &= 0x7f
	id := hex.EncodeToString(token)
	s := &scrcpyServer{app: app, serial: serial, remotePath: scrcpyJar, cancel: cancel, done: make(chan struct{})}
	fail := func(e error) (*scrcpyServer, error) {
		s.close()
		return nil, e
	}
	if e := app.ensureScrcpyJar(c, serial); e != nil {
		return fail(e)
	}
	options = scrcpyOptions(options)
	out, e := app.Command(c, "-s", serial, "forward", "tcp:0", "localabstract:scrcpy_"+id)
	if e != nil {
		return fail(e)
	}
	s.port = strings.TrimSpace(out)
	if _, e = strconv.Atoi(s.port); e != nil {
		return fail(errors.New(app.L("Puerto ADB no valido", "Invalid ADB port")))
	}
	if s.log, e = app.NewDiagnosticTail(); e != nil {
		return fail(e)
	}
	line := "CLASSPATH=" + app.Quote(s.remotePath) + " app_process / com.genymobile.scrcpy.Server 5.0.1 scid=" + id + " " + options
	s.cmd = exec.CommandContext(c, app.Adb, "-s", serial, "shell", line)
	s.cmd.Stdout = s.log
	s.cmd.Stderr = s.log
	if e = s.cmd.Start(); e != nil {
		s.cmd = nil
		return fail(e)
	}
	go func() {
		s.cmd.Wait()
		close(s.done)
	}()
	return s, nil
}

// connect opens the first socket. The ADB forward accepts before the server listens,
// so it retries until the server answers its dummy byte.
func (s *scrcpyServer) connect(c context.Context) (net.Conn, error) {
	app := s.app
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c.Err() != nil {
			return nil, c.Err()
		}
		conn, e := net.DialTimeout("tcp", "127.0.0.1:"+s.port, 200*time.Millisecond)
		if e == nil {
			if e = app.ReadHandshake(conn); e == nil {
				return conn, nil
			}
			conn.Close()
		}
		select {
		case <-s.done:
			return nil, errors.New(app.L("El servidor scrcpy se ha cerrado; consulta ", "The scrcpy server closed; see ") + s.log.String())
		case <-time.After(150 * time.Millisecond):
		}
	}
	return nil, errors.New(app.L("No responde el servidor de control", "The control server does not answer"))
}

// dial opens the next socket, once the first one answered.
func (s *scrcpyServer) dial() (net.Conn, error) {
	return net.DialTimeout("tcp", "127.0.0.1:"+s.port, 3*time.Second)
}

// close stops the server and removes its forward; its sockets go first. The jar stays
// for the next server.
func (s *scrcpyServer) close() {
	s.once.Do(func() {
		s.cancel()
		if s.cmd != nil {
			select {
			case <-s.done:
			case <-time.After(2 * time.Second):
			}
		}
		c, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if s.port != "" {
			s.app.Command(c, "-s", s.serial, "forward", "--remove", "tcp:"+s.port)
		}
		if s.log != nil {
			s.log.Close()
		}
	})
}
