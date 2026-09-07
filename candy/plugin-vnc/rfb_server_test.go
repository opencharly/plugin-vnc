package vnc

// rfb_server_test.go — a MINIMAL RFC 6143 RFB server stub, so the session
// recorder's DIAL path (NewVNCClient handshake + full non-incremental
// FramebufferUpdateRequest polling) is unit-tested over a REAL socket, not just
// the frameSource fake. The stub speaks just enough of the wire to be a real VNC
// server to this plugin's client: version 3.8 handshake with the None security
// type (no VeNCrypt/TLS in the stub), ServerInit, SetPixelFormat/SetEncodings
// reads, and Raw-encoded framebuffer updates for every FramebufferUpdateRequest.

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rfbServerStub is the minimal RFB server used by the recorder integration test.
type rfbServerStub struct {
	ln      net.Listener
	addr    string
	width   uint16
	height  uint16
	img     *image.RGBA // the framebuffer served for every update
	conns   []net.Conn
	handled chan struct{} // closed when the first client completed its handshake
}

// newRfbServerStub listens on 127.0.0.1:0 and serves the given framebuffer.
func newRfbServerStub(t *testing.T, w, h uint16, img *image.RGBA) *rfbServerStub {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("rfb stub listen: %v", err)
	}
	s := &rfbServerStub{
		ln:      ln,
		addr:    ln.Addr().String(),
		width:   w,
		height:  h,
		img:     img,
		handled: make(chan struct{}),
	}
	go s.acceptLoop()
	return s
}

func (s *rfbServerStub) acceptLoop() {
	first := true
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.conns = append(s.conns, conn)
		go func(c net.Conn, isFirst bool) {
			serveRfbConn(c, s, isFirst)
		}(conn, first)
		first = false
	}
}

func (s *rfbServerStub) Close() {
	_ = s.ln.Close()
	for _, c := range s.conns {
		_ = c.Close()
	}
}

// serveRfbConn runs the handshake + update loop for one client connection.
func serveRfbConn(conn net.Conn, s *rfbServerStub, isFirst bool) {
	defer conn.Close() //nolint:errcheck
	if _, err := conn.Write([]byte("RFB 003.008\n")); err != nil {
		return
	}
	var clientVersion [12]byte
	if _, err := io.ReadFull(conn, clientVersion[:]); err != nil {
		return
	}
	// Security handshake: offer ONLY None (type 1).
	if _, err := conn.Write([]byte{1, 1}); err != nil {
		return
	}
	var chosen uint8
	if err := binary.Read(conn, binary.BigEndian, &chosen); err != nil || chosen != 1 {
		return
	}
	// SecurityResult: OK.
	if err := binary.Write(conn, binary.BigEndian, uint32(0)); err != nil {
		return
	}
	// ClientInit (shared flag).
	var ci uint8
	if err := binary.Read(conn, binary.BigEndian, &ci); err != nil {
		return
	}
	_ = ci
	// ServerInit: 32-bit true-color little-endian framebuffer + desktop name.
	pf := vncPixelFormat{BPP: 32, Depth: 24, TrueColor: 1, RedMax: 255, GreenMax: 255, BlueMax: 255, RedShift: 16, GreenShift: 8, BlueShift: 0}
	name := []byte("opencharly-stub")
	if err := binary.Write(conn, binary.BigEndian, struct {
		Width  uint16
		Height uint16
		PF     vncPixelFormat
		NLen   uint32
	}{s.width, s.height, pf, uint32(len(name))}); err != nil {
		return
	}
	if _, err := conn.Write(name); err != nil {
		return
	}
	// The client's SetPixelFormat (20 bytes) + SetEncodings.
	msgType, err := readRfbMsg(conn)
	if err != nil {
		return
	}
	if msgType != 0 { // SetPixelFormat must come first
		return
	}
	if err := readRfbBody(conn, 19); err != nil { // 3 pad + 16-byte pf
		return
	}
	if msgType, err = readRfbMsg(conn); err != nil {
		return
	}
	if msgType != 2 { // SetEncodings
		return
	}
	// Header: 1 pad byte + num(uint16) — the pad MUST be read before num (R1:
	// skipping it shifted the read and ballooned the body read).
	var pad [1]byte
	if _, err := io.ReadFull(conn, pad[:]); err != nil {
		return
	}
	var num uint16
	if err := binary.Read(conn, binary.BigEndian, &num); err != nil {
		return
	}
	if err := readRfbBody(conn, int(num)*4); err != nil { // the encodings (int32 each)
		return
	}
	// Announce the first full handshake (the recorder's dial completed).
	if isFirst {
		close(s.handled)
	}
	if err := s.updateLoop(conn); err != nil {
		return
	}
}

// updateLoop answers every FramebufferUpdateRequest with one Raw-encoded
// full-framebuffer update.
func (s *rfbServerStub) updateLoop(conn net.Conn) error {
	for {
		msgType, err := readRfbMsg(conn)
		if err != nil {
			return err
		}
		switch msgType {
		case 3: // FramebufferUpdateRequest: 1 inc + 4 hdr bytes... actually 9 body bytes
			if err := readRfbBody(conn, 9); err != nil {
				return err
			}
			if err := binary.Write(conn, binary.BigEndian, struct {
				MsgType uint8
				Pad     uint8
				NumRect uint16
			}{0, 0, 1}); err != nil {
				return err
			}
			if err := binary.Write(conn, binary.BigEndian, struct {
				X, Y, W, H uint16
				Encoding   int32
			}{0, 0, s.width, s.height, 0}); err != nil {
				return err
			}
			bpp := 4
			data := make([]byte, int(s.width)*int(s.height)*bpp)
			for py := 0; py < int(s.height); py++ {
				for px := 0; px < int(s.width); px++ {
					c := s.img.RGBAAt(px, py)
					off := (py*int(s.width) + px) * bpp
					// Little-endian 32bpp with RedShift=16/GreenShift=8/BlueShift=0:
					// bytes are B, G, R, A.
					data[off] = c.B
					data[off+1] = c.G
					data[off+2] = c.R
					data[off+3] = 0
				}
			}
			if _, err := conn.Write(data); err != nil {
				return err
			}
		case 4, 5, 6: // KeyEvent / PointerEvent / ClientCutText — read + ignore
			if err := readRfbBody(conn, 12); err != nil {
				return err
			}
		default:
			return fmt.Errorf("rfb stub: unexpected client message %d", msgType)
		}
	}
}

// readRfbMsg reads one client-to-server message byte.
func readRfbMsg(conn net.Conn) (uint8, error) {
	var b [1]byte
	if _, err := io.ReadFull(conn, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func readRfbBody(conn net.Conn, n int) error {
	return readFull(conn, n)
}

func readFull(conn net.Conn, n int) error {
	buf := make([]byte, n)
	_, err := io.ReadFull(conn, buf)
	return err
}

// TestRunSessionRecorderAgainstRealRfbServer is the RFB framebuffer-polling path
// over a REAL socket: the recorder dials the stub server (full handshake),
// polls full framebuffer updates at fps into frames.mjpeg, and finalizes on the
// done close (the SIGTERM analog) with the FINAL marker + evidence row.json.
func TestRunSessionRecorderAgainstRealRfbServer(t *testing.T) {
	img := solidRGBA(32, 24, color.RGBA{R: 90, G: 140, B: 200, A: 255})
	srv := newRfbServerStub(t, 32, 24, img)
	defer srv.Close()
	stateDir := t.TempDir()
	done := make(chan struct{})
	cfg := RecorderConfig{
		Endpoint:  &vncEndpoint{Addr: srv.addr, Password: ""},
		Fps:       100,
		StateDir:  stateDir,
		SessionID: "bed.recorder.wire",
		Venue:     "check-some-pod",
		Phase:     "live",
	}
	type res struct {
		count int
		err   error
	}
	rc := make(chan res, 1)
	go func() {
		c, err := RunSessionRecorder(cfg, done)
		rc <- res{c, err}
	}()
	// Wait for the dial handshake to complete (or fail), then let frames accrue.
	select {
	case <-srv.handled:
	case <-time.After(5 * time.Second):
		t.Fatal("recorder never completed the RFB handshake with the stub server")
	}
	time.Sleep(50 * time.Millisecond)
	close(done)
	got := <-rc
	if got.err != nil {
		t.Fatalf("RunSessionRecorder over the real wire: %v", got.err)
	}
	if got.count < 2 {
		t.Fatalf("captured %d frames over the real wire, want >= 2", got.count)
	}

	mjpeg, err := os.ReadFile(filepath.Join(stateDir, framesFile))
	if err != nil {
		t.Fatalf("frames.mjpeg: %v", err)
	}
	frames := splitMJpeg(mjpeg)
	if len(frames) != got.count {
		t.Fatalf("splitMJpeg frames = %d, want %d", len(frames), got.count)
	}
	// Every decoded frame must carry the stub server's framebuffer dimensions.
	for i, fr := range frames {
		dec, err := decodeJpeg(fr)
		if err != nil {
			t.Fatalf("frame %d does not decode: %v", i, err)
		}
		b := dec.Bounds()
		if b.Dx() != 32 || b.Dy() != 24 {
			t.Fatalf("frame %d dimensions = %dx%d, want 32x24", i, b.Dx(), b.Dy())
		}
	}

	if _, err := os.Stat(filepath.Join(stateDir, finalMarker)); err != nil {
		t.Fatalf("FINAL marker missing after stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, evidenceFile)); err != nil {
		t.Fatalf("row.json missing after stop: %v", err)
	}
}
