package utils

import (
	"bytes"
	"crypto/tls"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh"
)

// SSHConnection handles state for a SSHConnection. It wraps an ssh.ServerConn
// and allows us to pass other state around the application.
// Listeners is a map[string]net.Listener.
type SSHConnection struct {
	SSHConn        *ssh.ServerConn
	Listeners      *sync.Map
	Closed         *sync.Once
	Close          chan bool
	Exec           chan bool
	Messages       chan string
	ProxyProto     byte
	HostHeader     string
	StripPath      bool
	SNIProxy       bool
	TCPAlias       bool
	LocalForward   bool
	Session        chan bool
	CleanupHandler bool
	SetupLock      *sync.Mutex
}

// SendMessage sends a console message to the connection. If block is true, it
// will block until the message is sent. If it is false, it will try to send the
// message 5 times, waiting 100ms each time.
func (s *SSHConnection) SendMessage(message string, block bool) {
	if block {
		s.Messages <- message
		return
	}

	for i := 0; i < 5; {
		select {
		case <-s.Close:
			return
		case s.Messages <- message:
			return
		default:
			time.Sleep(100 * time.Millisecond)
			i++
		}
	}
}

// CleanUp closes all allocated resources for a SSH session and cleans them up.
func (s *SSHConnection) CleanUp(state *State) {
	s.Closed.Do(func() {
		close(s.Close)
		s.SSHConn.Close()
		state.SSHConnections.Delete(s.SSHConn.RemoteAddr().String())
		log.Println("Closed SSH connection for:", s.SSHConn.RemoteAddr().String(), "user:", s.SSHConn.User())
	})
}

// TeeConn represents a simple net.Conn interface for SNI Processing.
type TeeConn struct {
	Reader io.Reader
	Buffer *bytes.Buffer
}

// Read implements a reader ontop of the TeeReader.
func (conn TeeConn) Read(p []byte) (int, error) { return conn.Reader.Read(p) }

// Write is a shim function to fit net.Conn.
func (conn TeeConn) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

// Close is a shim function to fit net.Conn.
func (conn TeeConn) Close() error { return nil }

// LocalAddr is a shim function to fit net.Conn.
func (conn TeeConn) LocalAddr() net.Addr { return nil }

// RemoteAddr is a shim function to fit net.Conn.
func (conn TeeConn) RemoteAddr() net.Addr { return nil }

// SetDeadline is a shim function to fit net.Conn.
func (conn TeeConn) SetDeadline(t time.Time) error { return nil }

// SetReadDeadline is a shim function to fit net.Conn.
func (conn TeeConn) SetReadDeadline(t time.Time) error { return nil }

// SetWriteDeadline is a shim function to fit net.Conn.
func (conn TeeConn) SetWriteDeadline(t time.Time) error { return nil }

// GetBuffer returns the tee'd buffer.
func (conn TeeConn) GetBuffer() *bytes.Buffer { return conn.Buffer }

func NewTeeConn(reader io.Reader) TeeConn {
	teeConn := TeeConn{
		Buffer: bytes.NewBuffer([]byte{}),
	}

	teeConn.Reader = io.TeeReader(reader, teeConn.Buffer)

	return teeConn
}

// PeakTLSHello peaks the TLS Connection Hello to proxy based on SNI.
func PeakTLSHello(reader io.Reader) (*tls.ClientHelloInfo, *bytes.Buffer, error) {
	var tlsHello *tls.ClientHelloInfo

	tlsConfig := &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			tlsHello = hello
			return nil, nil
		},
	}

	teeConn := NewTeeConn(reader)

	err := tls.Server(teeConn, tlsConfig).Handshake()

	return tlsHello, teeConn.GetBuffer(), err
}

// IdleTimeoutConn handles the connection with a context deadline.
// code adapted from https://qiita.com/kwi/items/b38d6273624ad3f6ae79
type IdleTimeoutConn struct {
	Conn net.Conn
}

// Read is needed to implement the reader part.
func (i IdleTimeoutConn) Read(buf []byte) (int, error) {
	err := i.Conn.SetReadDeadline(time.Now().Add(viper.GetDuration("idle-connection-timeout")))
	if err != nil {
		return 0, err
	}

	return i.Conn.Read(buf)
}

// Write is needed to implement the writer part.
func (i IdleTimeoutConn) Write(buf []byte) (int, error) {
	err := i.Conn.SetWriteDeadline(time.Now().Add(viper.GetDuration("idle-connection-timeout")))
	if err != nil {
		return 0, err
	}

	return i.Conn.Write(buf)
}

// CopyBoth copies betwen a reader and writer and will cleanup each.
func CopyBoth(writer net.Conn, reader io.ReadWriteCloser) {
	closeBoth := func() {
		reader.Close()
		writer.Close()
	}

	var tcon io.ReadWriter

	if viper.GetBool("idle-connection") {
		tcon = IdleTimeoutConn{
			Conn: writer,
		}
	} else {
		tcon = writer
	}

	copyToReader := func() {
		_, err := io.Copy(reader, tcon)
		if err != nil && viper.GetBool("debug") {
			log.Println("Error copying to reader:", err)
		}

		closeBoth()
	}

	copyToWriter := func() {
		_, err := io.Copy(tcon, reader)
		if err != nil && viper.GetBool("debug") {
			log.Println("Error copying to writer:", err)
		}

		closeBoth()
	}

	go copyToReader()
	copyToWriter()
}
