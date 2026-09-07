package vnc

// recorder.go — the DETACHED host-side session recorder (plan Cutover E, E-1).
// `vnc: session start` hands THIS binary (in recorder mode, env
// CHARLY_VNC_RECORDER=1) to the runner's generic background-session service
// (plugin-check's compiled-in verb:session seam). The recorder owns the RFB wire
// for the whole session: it dials the host-pre-resolved endpoint, polls the
// framebuffer at the session fps into <state_dir>/frames.mjpeg (each poll is one
// full non-incremental FramebufferUpdateRequest decoded to an image and encoded
// as a JPEG — the VNC analogue of the spice record loop's every-poll capture),
// and on SIGTERM/SIGINT finalizes: the deterministic FINAL marker + the evidence
// row.json ("instrument"/"origin"/"verb"/"artifact" — the shared #EvidenceRow
// shape, plan §4 A-task-1). While it runs, the PROVIDER spawns no process, knows
// no transport, and owns no pidfile.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Recorder-mode env contract between provider.go (the spawn env) and cmd/serve's
// recorder mode (the reader). The provider builds these keys in buildSessionSpawn.
const (
	EnvRecorder  = "CHARLY_VNC_RECORDER"
	EnvEndpoint  = "CHARLY_VNC_ENDPOINT"
	EnvFps       = "CHARLY_VNC_FPS"
	EnvStateDir  = "CHARLY_VNC_STATE_DIR"
	EnvSessionID = "CHARLY_VNC_SESSION_ID"
	EnvVenue     = "CHARLY_VNC_VENUE"
	EnvPhase     = "CHARLY_VNC_PHASE"
)

// framesFile is the MJPEG artifact name inside the session state dir; finalMarker
// is the deterministic end-of-stream marker the stop path greps for.
const (
	framesFile   = "frames.mjpeg"
	finalMarker  = "FINAL"
	evidenceFile = "row.json"
)

// evidenceRow mirrors the shared #EvidenceRow shape (plan §4 A-task-1) — the
// minimal session subset the recorder writes and sessionStop reads back. No
// plugin-specific manifest code: the runner's evidence phase consumes the general
// shape.
type evidenceRow struct {
	Instrument string             `json:"instrument"`
	Origin     string             `json:"origin"`
	Verb       string             `json:"verb"`
	Venue      string             `json:"venue,omitempty"`
	Phase      string             `json:"phase,omitempty"`
	Artifact   []evidenceArtifact `json:"artifact,omitempty"`
}

type evidenceArtifact struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// ParseEndpoint decodes the endpoint JSON the provider threads via
// CHARLY_VNC_ENDPOINT (the vncEndpoint wire shape).
func ParseEndpoint(raw []byte) (*vncEndpoint, error) {
	var ep vncEndpoint
	if err := json.Unmarshal(raw, &ep); err != nil {
		return nil, fmt.Errorf("decode endpoint JSON: %w", err)
	}
	return &ep, nil
}

// RecorderConfig is the detached-session recorder's full runtime contract.
type RecorderConfig struct {
	Endpoint  *vncEndpoint
	Fps       int    // frames/second; 0 defaults to 5 (mirrors the record-loop default)
	StateDir  string // the run's state dir: frames.mjpeg + FINAL + row.json land here
	SessionID string // the venue-scoped session id — stamped into the evidence row
	Venue     string // evidence-row provenance
	Phase     string // evidence-row provenance (build|live|update|teardown)
}

// frameSource is the subset of VNCClient the recorder consumes: one full
// (non-incremental) RFB framebuffer poll. VNCClient.Screenshot satisfies it
// directly; the poll loop is unit-testable with a fake (B12).
type frameSource interface {
	Screenshot() (image.Image, error)
}

// RunSessionRecorder is the detached-mode engine (cmd/serve, recorder mode): dials
// the endpoint, polls the framebuffer into frames.mjpeg until done closes, then
// finalizes the FINAL marker + row.json. Returns the captured frame count.
func RunSessionRecorder(cfg RecorderConfig, done <-chan struct{}) (int, error) {
	if cfg.Endpoint == nil {
		return 0, fmt.Errorf("recorder: nil endpoint")
	}
	c, err := NewVNCClient(cfg.Endpoint.Addr, cfg.Endpoint.Password)
	if err != nil {
		return 0, fmt.Errorf("recorder: dial endpoint: %w", err)
	}
	defer c.Close() //nolint:errcheck
	return captureSession(c, cfg, done)
}

// captureSession is the recorder core, unit-testable with the frame fake: polls
// the framebuffer source at the session fps, appending every frame as a JPEG into
// <state_dir>/frames.mjpeg until done closes, then finalizes. Returns the frame
// count.
func captureSession(s frameSource, cfg RecorderConfig, done <-chan struct{}) (int, error) {
	if cfg.StateDir == "" {
		return 0, fmt.Errorf("recorder: empty state dir")
	}
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return 0, fmt.Errorf("recorder: create state dir: %w", err)
	}
	out, err := os.Create(filepath.Join(cfg.StateDir, framesFile))
	if err != nil {
		return 0, fmt.Errorf("recorder: open %s: %w", framesFile, err)
	}
	count := writeFrames(s, captureInterval(cfg.Fps), out, done)
	if err := out.Close(); err != nil {
		return 0, fmt.Errorf("recorder: close %s: %w", framesFile, err)
	}
	if err := finalizeSession(cfg, count); err != nil {
		return 0, err
	}
	return count, nil
}

// captureInterval maps a fps int to a poll interval (default 5 fps; 100 fps cap —
// identical semantics to the record loop's interval).
func captureInterval(fps int) time.Duration {
	if fps <= 0 {
		fps = 5
	}
	d := time.Second / time.Duration(fps)
	if d < 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	return d
}

// writeFrames polls the framebuffer source at interval, appending each frame as a
// JPEG onto w until done closes. Video semantics identical to the record loop:
// every poll is one frame of the stream (a full framebuffer request/response per
// frame; a poll that errors is skipped — a broken wire just stops appending).
// Returns the frame count.
func writeFrames(s frameSource, interval time.Duration, w io.Writer, done <-chan struct{}) int {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	count := 0
	for {
		select {
		case <-done:
			return count
		case <-tick.C:
			img, err := s.Screenshot()
			if err != nil || img == nil {
				continue
			}
			b := encodeFrame(img)
			if len(b) == 0 {
				continue
			}
			w.Write(b)
			count++
		}
	}
}

// finalizeSession writes the deterministic end-of-stream marker + the evidence row
// into the state dir. Called once, on the SIGTERM path — the runner's stop is
// complete only when row.json is on disk.
func finalizeSession(cfg RecorderConfig, count int) error {
	marker := fmt.Sprintf("final frames=%d\n", count)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, finalMarker), []byte(marker), 0o644); err != nil {
		return fmt.Errorf("recorder: write %s: %w", finalMarker, err)
	}
	row := evidenceRow{
		Instrument: cfg.SessionID,
		Origin:     "session",
		Verb:       "vnc",
		Venue:      cfg.Venue,
		Phase:      cfg.Phase,
		Artifact: []evidenceArtifact{{
			Path: filepath.Join(cfg.StateDir, framesFile),
			Kind: "mjpeg",
		}},
	}
	b, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		return fmt.Errorf("recorder: marshal evidence row: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(cfg.StateDir, evidenceFile), b, 0o644); err != nil {
		return fmt.Errorf("recorder: write %s: %w", evidenceFile, err)
	}
	return nil
}

// encodeFrame encodes one display frame as a JPEG — the recorder's ONE encoder
// (a nil return means the encoder rejected the frame).
func encodeFrame(img image.Image) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil
	}
	return b.Bytes()
}
