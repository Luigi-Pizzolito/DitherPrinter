package capture

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

type PortalCapture struct {
	mu            sync.RWMutex
	latest        *image.RGBA
	frameID       uint64
	status        string
	running       bool
	cmd           *exec.Cmd
	cancel        context.CancelFunc
	dbusConn      *dbus.Conn
	sessionHandle dbus.ObjectPath
	stderrBuffer  *bytes.Buffer
}

func NewPortalCapture() *PortalCapture {
	return &PortalCapture{status: "Capture idle"}
}

func (capture *PortalCapture) Start() error {
	capture.mu.Lock()
	if capture.running {
		capture.mu.Unlock()
		return nil
	}
	capture.status = "Requesting screen capture permission..."
	capture.mu.Unlock()

	conn, sessionHandle, nodeID, pipewireFD, err := startPortalSession()
	if err != nil {
		message := fmt.Sprintf("Portal error: %v", err)
		logCaptureError(message)
		capture.setStatus(message)
		return err
	}

	const frameWidth = 960
	const frameHeight = 540

	if _, pathErr := exec.LookPath("gst-launch-1.0"); pathErr != nil {
		closePortalSession(conn, sessionHandle)
		logCaptureError("Capture error: gst-launch-1.0 not found")
		capture.setStatus("Capture error: gst-launch-1.0 not found")
		return errors.New("gst-launch-1.0 not found; install GStreamer with pipewire plugin")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(
		ctx,
		"gst-launch-1.0",
		"-q",
		"pipewiresrc",
		"fd=3",
		fmt.Sprintf("path=%d", nodeID),
		"always-copy=true",
		"do-timestamp=true",
		"!",
		"queue",
		"!",
		"videoconvert",
		"!",
		"videoscale",
		"!",
		"video/x-raw,format=RGBA,width=960,height=540",
		"!",
		"fdsink",
		"fd=1",
		"sync=false",
	)
	logCaptureError("Starting GStreamer capture: node=%d, target=%dx%d", nodeID, frameWidth, frameHeight)

	pipewireFile := os.NewFile(uintptr(pipewireFD), "pipewire-remote")
	if pipewireFile == nil {
		cancel()
		closePortalSession(conn, sessionHandle)
		logCaptureError("Capture error: invalid PipeWire remote fd")
		capture.setStatus("Capture error: invalid PipeWire remote fd")
		return errors.New("invalid PipeWire remote fd")
	}
	cmd.ExtraFiles = []*os.File{pipewireFile}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = pipewireFile.Close()
		cancel()
		closePortalSession(conn, sessionHandle)
		message := fmt.Sprintf("capture stdout error: %v", err)
		logCaptureError(message)
		capture.setStatus(message)
		return err
	}

	stderrBuffer := &bytes.Buffer{}
	cmd.Stderr = stderrBuffer

	if err = cmd.Start(); err != nil {
		_ = pipewireFile.Close()
		cancel()
		closePortalSession(conn, sessionHandle)
		message := fmt.Sprintf("capture start error: %v", err)
		logCaptureError(message)
		capture.setStatus(message)
		return err
	}
	_ = pipewireFile.Close()

	capture.mu.Lock()
	capture.running = true
	capture.cmd = cmd
	capture.cancel = cancel
	capture.dbusConn = conn
	capture.sessionHandle = sessionHandle
	capture.stderrBuffer = stderrBuffer
	capture.status = fmt.Sprintf("Capturing selected window (node %d via GStreamer)", nodeID)
	capture.mu.Unlock()

	go capture.readFrames(stdout, frameWidth, frameHeight)
	go capture.waitForProcessEnd()

	return nil
}

func (capture *PortalCapture) Stop() {
	capture.mu.Lock()
	if !capture.running {
		capture.mu.Unlock()
		return
	}
	cancel := capture.cancel
	cmd := capture.cmd
	conn := capture.dbusConn
	sessionHandle := capture.sessionHandle
	capture.running = false
	capture.cancel = nil
	capture.cmd = nil
	capture.dbusConn = nil
	capture.sessionHandle = ""
	capture.stderrBuffer = nil
	capture.status = "Capture stopped"
	capture.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	if conn != nil {
		closePortalSession(conn, sessionHandle)
	}
}

func (capture *PortalCapture) IsRunning() bool {
	capture.mu.RLock()
	defer capture.mu.RUnlock()
	return capture.running
}

func (capture *PortalCapture) Status() string {
	capture.mu.RLock()
	defer capture.mu.RUnlock()
	return capture.status
}

func (capture *PortalCapture) FrameID() uint64 {
	return atomic.LoadUint64(&capture.frameID)
}

func (capture *PortalCapture) LatestFrame() *image.RGBA {
	capture.mu.RLock()
	defer capture.mu.RUnlock()
	return capture.latest
}

func (capture *PortalCapture) readFrames(stdoutPipe interface{ Read([]byte) (int, error) }, width, height int) {
	reader := bufio.NewReader(stdoutPipe)
	frameSize := width * height * 4
	buffer := make([]byte, frameSize)

	for {
		_, err := readFull(reader, buffer)
		if err != nil {
			logCaptureError("Capture stream ended: %v", err)
			capture.setStatus("Capture stream ended")
			return
		}

		frame := image.NewRGBA(image.Rect(0, 0, width, height))
		copy(frame.Pix, buffer)

		capture.mu.Lock()
		capture.latest = frame
		capture.mu.Unlock()
		atomic.AddUint64(&capture.frameID, 1)
	}
}

func (capture *PortalCapture) waitForProcessEnd() {
	capture.mu.RLock()
	cmd := capture.cmd
	stderrBuffer := capture.stderrBuffer
	capture.mu.RUnlock()
	if cmd == nil {
		return
	}

	err := cmd.Wait()
	capture.mu.Lock()
	wasRunning := capture.running
	capture.running = false
	capture.cmd = nil
	capture.cancel = nil
	capture.stderrBuffer = nil
	capture.mu.Unlock()

	if wasRunning && err != nil {
		stderrText := ""
		if stderrBuffer != nil {
			stderrText = strings.TrimSpace(stderrBuffer.String())
		}
		if stderrText != "" {
			logCaptureError("Capture process exited with error: %v", err)
			logCaptureError("Capture stderr:\n%s", stderrText)
		} else {
			logCaptureError("Capture process exited with error: %v", err)
		}
		if stderrText != "" {
			capture.setStatus(fmt.Sprintf("Capture stopped: %v (%s)", err, tailLine(stderrText)))
		} else {
			capture.setStatus(fmt.Sprintf("Capture stopped: %v", err))
		}
	}
}

func tailLine(value string) string {
	parts := strings.Split(value, "\n")
	if len(parts) == 0 {
		return value
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func (capture *PortalCapture) setStatus(status string) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.status = status
}

func logCaptureError(format string, args ...interface{}) {
	log.Printf("[capture] "+format, args...)
}

func readFull(reader *bufio.Reader, buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		n, err := reader.Read(buffer[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func startPortalSession() (*dbus.Conn, dbus.ObjectPath, uint32, dbus.UnixFD, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, "", 0, -1, err
	}

	object := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	baseToken := strconv.FormatInt(time.Now().UnixNano(), 10)

	createReq, err := portalRequest(conn, object, "org.freedesktop.portal.ScreenCast.CreateSession", map[string]dbus.Variant{
		"session_handle_token": dbus.MakeVariant("session" + baseToken),
		"handle_token":         dbus.MakeVariant("handle" + baseToken + "c"),
	})
	if err != nil {
		conn.Close()
		return nil, "", 0, -1, err
	}

	createResponse, err := waitPortalResponse(conn, createReq, 60*time.Second)
	if err != nil {
		conn.Close()
		return nil, "", 0, -1, err
	}

	sessionHandle, ok := createResponse["session_handle"]
	if !ok {
		conn.Close()
		return nil, "", 0, -1, errors.New("portal response missing session_handle")
	}

	sessionPath, err := extractSessionPath(sessionHandle)
	if err != nil {
		conn.Close()
		return nil, "", 0, -1, err
	}

	selectReq, err := portalRequest(conn, object, "org.freedesktop.portal.ScreenCast.SelectSources", sessionPath, map[string]dbus.Variant{
		"types":        dbus.MakeVariant(uint32(2)),
		"multiple":     dbus.MakeVariant(false),
		"cursor_mode":  dbus.MakeVariant(uint32(2)),
		"handle_token": dbus.MakeVariant("handle" + baseToken + "s"),
	})
	if err != nil {
		closePortalSession(conn, sessionPath)
		return nil, "", 0, -1, err
	}

	if _, err = waitPortalResponse(conn, selectReq, 60*time.Second); err != nil {
		closePortalSession(conn, sessionPath)
		return nil, "", 0, -1, err
	}

	startReq, err := portalRequest(conn, object, "org.freedesktop.portal.ScreenCast.Start", sessionPath, "", map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant("handle" + baseToken + "t"),
	})
	if err != nil {
		closePortalSession(conn, sessionPath)
		return nil, "", 0, -1, err
	}

	startResponse, err := waitPortalResponse(conn, startReq, 60*time.Second)
	if err != nil {
		closePortalSession(conn, sessionPath)
		return nil, "", 0, -1, err
	}

	streamsVariant, ok := startResponse["streams"]
	if !ok {
		closePortalSession(conn, sessionPath)
		return nil, "", 0, -1, errors.New("portal response missing streams")
	}

	nodeID, err := extractNodeID(streamsVariant.Value())
	if err != nil {
		closePortalSession(conn, sessionPath)
		return nil, "", 0, -1, err
	}

	pipewireFD, err := openPipeWireRemote(conn, object, sessionPath)
	if err != nil {
		closePortalSession(conn, sessionPath)
		return nil, "", 0, -1, err
	}

	return conn, sessionPath, nodeID, pipewireFD, nil
}

func openPipeWireRemote(conn *dbus.Conn, object dbus.BusObject, sessionPath dbus.ObjectPath) (dbus.UnixFD, error) {
	call := object.Call("org.freedesktop.portal.ScreenCast.OpenPipeWireRemote", 0, sessionPath, map[string]dbus.Variant{})
	if call.Err != nil {
		return -1, call.Err
	}
	var pipewireFD dbus.UnixFD
	if err := call.Store(&pipewireFD); err != nil {
		return -1, err
	}
	if pipewireFD < 0 {
		return -1, errors.New("portal returned invalid PipeWire remote fd")
	}
	return pipewireFD, nil
}

func portalRequest(conn *dbus.Conn, object dbus.BusObject, method string, args ...interface{}) (dbus.ObjectPath, error) {
	call := object.Call(method, 0, args...)
	if call.Err != nil {
		return "", call.Err
	}
	var requestPath dbus.ObjectPath
	if err := call.Store(&requestPath); err != nil {
		return "", err
	}

	matchRule := fmt.Sprintf("type='signal',interface='org.freedesktop.portal.Request',member='Response',path='%s'", requestPath)
	if matchErr := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRule).Err; matchErr != nil {
		return "", matchErr
	}

	return requestPath, nil
}

func waitPortalResponse(conn *dbus.Conn, requestPath dbus.ObjectPath, timeout time.Duration) (map[string]dbus.Variant, error) {
	signalChannel := make(chan *dbus.Signal, 8)
	conn.Signal(signalChannel)
	defer conn.RemoveSignal(signalChannel)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case signal := <-signalChannel:
			if signal == nil || signal.Path != requestPath || len(signal.Body) < 2 {
				continue
			}
			responseCode, ok := signal.Body[0].(uint32)
			if !ok {
				continue
			}
			results, ok := signal.Body[1].(map[string]dbus.Variant)
			if !ok {
				return nil, errors.New("invalid portal response payload")
			}
			if responseCode != 0 {
				if responseCode == 1 {
					return nil, errors.New("capture request was cancelled")
				}
				return nil, fmt.Errorf("portal returned response code %d", responseCode)
			}
			return results, nil
		case <-timer.C:
			return nil, errors.New("portal response timed out")
		}
	}
}

func closePortalSession(conn *dbus.Conn, sessionPath dbus.ObjectPath) {
	if conn != nil && sessionPath != "" {
		session := conn.Object("org.freedesktop.portal.Desktop", sessionPath)
		_ = session.Call("org.freedesktop.portal.Session.Close", 0).Err
		conn.Close()
	}
}

func extractSessionPath(sessionHandle dbus.Variant) (dbus.ObjectPath, error) {
	value := sessionHandle.Value()
	for {
		if nested, ok := value.(dbus.Variant); ok {
			value = nested.Value()
			continue
		}
		break
	}

	switch handle := value.(type) {
	case dbus.ObjectPath:
		if handle == "" {
			return "", errors.New("empty session_handle")
		}
		return handle, nil
	case string:
		if handle == "" {
			return "", errors.New("empty session_handle")
		}
		return dbus.ObjectPath(handle), nil
	default:
		return "", fmt.Errorf("invalid session_handle type %T", value)
	}
}

func extractNodeID(value interface{}) (uint32, error) {
	streams := unwrapValue(value)
	rv := reflect.ValueOf(streams)
	if !rv.IsValid() {
		return 0, errors.New("portal returned empty streams")
	}

	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return 0, fmt.Errorf("unexpected streams container type: %T", streams)
	}
	if rv.Len() == 0 {
		return 0, errors.New("portal returned no streams")
	}

	firstStream := unwrapReflectValue(rv.Index(0))
	nodeID, ok := extractFirstTupleElementUint32(firstStream)
	if ok {
		return nodeID, nil
	}

	if mapping, ok := streams.(map[string]interface{}); ok {
		if candidate, found := mapping["node_id"]; found {
			if nodeID, ok := toUint32(unwrapValue(candidate)); ok {
				return nodeID, nil
			}
		}
	}

	return 0, fmt.Errorf("unexpected streams response shape: %T", value)
}

func extractFirstTupleElementUint32(value interface{}) (uint32, bool) {
	value = unwrapValue(value)
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return 0, false
	}

	switch rv.Kind() {
	case reflect.Struct:
		if rv.NumField() == 0 {
			return 0, false
		}
		field := unwrapReflectValue(rv.Field(0))
		return toUint32(field)
	case reflect.Slice, reflect.Array:
		if rv.Len() == 0 {
			return 0, false
		}
		field := unwrapReflectValue(rv.Index(0))
		return toUint32(field)
	default:
		return toUint32(value)
	}
}

func unwrapValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}

	for {
		variant, ok := value.(dbus.Variant)
		if !ok {
			break
		}
		value = variant.Value()
	}
	return value
}

func unwrapReflectValue(value reflect.Value) interface{} {
	if !value.IsValid() {
		return nil
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.CanInterface() {
		return nil
	}
	return unwrapValue(value.Interface())
}

func toUint32(value interface{}) (uint32, bool) {
	if value == nil {
		return 0, false
	}

	switch typed := value.(type) {
	case uint32:
		return typed, true
	case uint:
		return uint32(typed), true
	case uint64:
		return uint32(typed), true
	case uint16:
		return uint32(typed), true
	case uint8:
		return uint32(typed), true
	case int:
		if typed >= 0 {
			return uint32(typed), true
		}
		return 0, false
	case int64:
		if typed >= 0 {
			return uint32(typed), true
		}
		return 0, false
	case int32:
		if typed >= 0 {
			return uint32(typed), true
		}
		return 0, false
	case int16:
		if typed >= 0 {
			return uint32(typed), true
		}
		return 0, false
	case int8:
		if typed >= 0 {
			return uint32(typed), true
		}
		return 0, false
	}

	return 0, false
}
