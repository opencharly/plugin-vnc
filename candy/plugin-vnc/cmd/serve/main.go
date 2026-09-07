// Command serve is the OUT-OF-PROCESS entrypoint for the vnc verb plugin: a thin
// shim serving the importable provider over go-plugin gRPC via sdk.Serve. The SAME
// NewProvider()/NewMeta() compile INTO charly in-process when listed in
// compiled_plugins; this binary is host-built + connected only when they are NOT —
// placement is invisible above the registry.
//
// HIDDEN RECORDER MODE (plan Cutover E, E-1): with CHARLY_VNC_RECORDER=1 the SAME
// binary skips serving and becomes the DETACHED host-side session recorder — the
// runner's generic background-session service spawns it for a vnc: session start. It
// dials the RFB endpoint from env, polls the framebuffer at fps into
// $CHARLY_VNC_STATE_DIR/frames.mjpeg, and on SIGTERM/SIGINT finalizes (FINAL
// marker + evidence row.json) before exiting 0: the runner's stop is complete only
// when row.json is on disk.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	vnc "github.com/opencharly/plugin-vnc/candy/plugin-vnc"
	"github.com/opencharly/sdk"
)

func main() {
	if os.Getenv(vnc.EnvRecorder) == "1" {
		os.Exit(recorderMain())
	}
	sdk.Serve(vnc.NewProvider(), vnc.NewMeta())
}

// recorderMain is the detached recorder process entrypoint (see the package doc). It
// returns the process exit code.
func recorderMain() int {
	endpointJSON := os.Getenv(vnc.EnvEndpoint)
	stateDir := os.Getenv(vnc.EnvStateDir)
	sessionID := os.Getenv(vnc.EnvSessionID)
	if endpointJSON == "" || stateDir == "" || sessionID == "" {
		fmt.Fprintf(os.Stderr, "charly-vnc recorder: missing env (endpoint=%q state_dir=%q session_id=%q)\n", endpointJSON, stateDir, sessionID)
		return 2
	}
	ep, err := vnc.ParseEndpoint([]byte(endpointJSON))
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly-vnc recorder: %v\n", err)
		return 2
	}
	fps, _ := strconv.Atoi(os.Getenv(vnc.EnvFps))
	cfg := vnc.RecorderConfig{
		Endpoint:  ep,
		Fps:       fps,
		StateDir:  stateDir,
		SessionID: sessionID,
		Venue:     os.Getenv(vnc.EnvVenue),
		Phase:     os.Getenv(vnc.EnvPhase),
	}

	done := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		close(done) // deterministic finalize: FINAL marker + row.json
	}()

	count, err := vnc.RunSessionRecorder(cfg, done)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly-vnc recorder: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "charly-vnc recorder: finalized session %s frames=%d state_dir=%s\n", sessionID, count, stateDir)
	return 0
}
