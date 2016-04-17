package dev

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/udhos/jazigo/conf"
)

type Model struct {
	name        string
	defaultAttr attributes
}

type attributes struct {
	needLoginChat               bool     // need login chat
	needEnabledMode             bool     // need enabled mode
	needPagingOff               bool     // need disabled pager
	enableCommand               string   // enable
	usernamePromptPattern       string   // Username:
	passwordPromptPattern       string   // Password:
	enablePasswordPromptPattern string   // Password:
	disabledPromptPattern       string   // >
	enabledPromptPattern        string   // #
	commandList                 []string // show run
	disablePagerCommand         string   // term len 0

	// readTimeout: per-read timeout (protection against inactivity)
	// matchTimeout: full match timeout (protection against slow sender -- think 1 byte per second)
	readTimeout         time.Duration // protection against inactivity
	matchTimeout        time.Duration // protection against slow sender
	sendTimeout         time.Duration // protection against inactivity
	commandReadTimeout  time.Duration // larger timeout for slow responses (slow show running)
	commandMatchTimeout time.Duration // larger timeout for slow responses (slow show running)
}

type Device struct {
	devModel   *Model
	id         string
	hostPort   string
	transports string

	loginUser      string
	loginPassword  string
	enablePassword string

	attr attributes

	lastStatus  bool // true=good false=bad
	lastTry     time.Time
	lastSuccess time.Time
}

func (d *Device) Model() string {
	return d.devModel.name
}

func (d *Device) Id() string {
	return d.id
}

func (d *Device) Host() string {
	return d.hostPort
}

func (d *Device) Transport() string {
	return d.transports
}

func (d *Device) LastStatus() bool {
	return d.lastStatus
}

func (d *Device) LastTry() time.Time {
	return d.lastTry
}

func (d *Device) LastSuccess() time.Time {
	return d.lastSuccess
}

func (d *Device) Holdtime(now time.Time, holdtime int) time.Duration {
	return time.Duration(holdtime)*time.Second - now.Sub(d.lastSuccess)
}

const TEMP_REPO = "/tmp/tmp-jazigo-repo"

func tempRepo() string {
	path := TEMP_REPO
	if err := os.MkdirAll(path, 0700); err != nil {
		panic(fmt.Sprintf("tempRepo: '%s': %v", path, err))
	}
	return path
}

func cleanupTempRepo() string {
	path := TEMP_REPO
	if err := os.RemoveAll(path); err != nil {
		panic(fmt.Sprintf("cleanupTempRepo: '%s': %v", path, err))
	}
	return path
}

func RegisterModels(logger hasPrintf, t *DeviceTable) {
	registerModelCiscoIOS(logger, t)
	registerModelLinux(logger, t)
	registerModelJunOS(logger, t)
	registerModelHTTP(logger, t)
}

func CreateDevice(tab *DeviceTable, logger hasPrintf, modelName, id, hostPort, transports, user, pass, enable string) {
	logger.Printf("CreateDevice: %s %s %s %s", modelName, id, hostPort, transports)

	mod, getErr := tab.GetModel(modelName)
	if getErr != nil {
		logger.Printf("CreateDevice: could not find model '%s': %v", modelName, getErr)
		return
	}

	d := NewDevice(mod, id, hostPort, transports, user, pass, enable)

	if newDevErr := tab.SetDevice(d); newDevErr != nil {
		logger.Printf("CreateDevice: could not add device '%s': %v", id, newDevErr)
	}
}

func NewDevice(mod *Model, id, hostPort, transports, loginUser, loginPassword, enablePassword string) *Device {
	d := &Device{devModel: mod, id: id, hostPort: hostPort, transports: transports, loginUser: loginUser, loginPassword: loginPassword, enablePassword: enablePassword}
	d.attr = mod.defaultAttr
	return d
}

const (
	FETCH_ERR_NONE     = 0
	FETCH_ERR_TRANSP   = 1
	FETCH_ERR_LOGIN    = 2
	FETCH_ERR_ENABLE   = 3
	FETCH_ERR_PAGER    = 4
	FETCH_ERR_COMMANDS = 5
	FETCH_ERR_SAVE     = 6
)

type FetchResult struct {
	Model       string
	DevId       string
	DevHostPort string
	Transport   string
	Msg         string    // result error message
	Code        int       // result error code
	Begin       time.Time // begin timestamp
}

type hasPrintf interface {
	Printf(fmt string, v ...interface{})
}

type dialog struct {
	buf  []byte
	save [][]byte
}

func (d *dialog) record(buf []byte) {
	//d.buf = append(d.buf, buf...) // record full input
}

// fetch runs in a per-device goroutine
func (d *Device) Fetch(logger hasPrintf, resultCh chan FetchResult, delay time.Duration, repository string, maxFiles int) {
	modelName := d.devModel.name
	logger.Printf("fetch: %s %s %s %s delay=%dms", modelName, d.id, d.hostPort, d.transports, delay/time.Millisecond)

	if delay > 0 {
		time.Sleep(delay)
	}

	begin := time.Now()

	session, transport, logged, err := openTransport(logger, modelName, d.id, d.hostPort, d.transports, d.loginUser, d.loginPassword)
	if err != nil {
		resultCh <- FetchResult{Model: modelName, DevId: d.id, DevHostPort: d.hostPort, Transport: transport, Msg: fmt.Sprintf("fetch transport: %v", err), Code: FETCH_ERR_TRANSP, Begin: begin}
		return
	}

	defer session.Close()

	logger.Printf("fetch: %s %s %s - transport OPEN logged=%v", modelName, d.id, d.hostPort, logged)

	capture := dialog{}

	enabled := false

	if d.attr.needLoginChat && !logged {
		e, loginErr := d.login(logger, session, &capture)
		if loginErr != nil {
			resultCh <- FetchResult{Model: modelName, DevId: d.id, DevHostPort: d.hostPort, Transport: transport, Msg: fmt.Sprintf("fetch login: %v", loginErr), Code: FETCH_ERR_LOGIN, Begin: begin}
			return
		}
		if e {
			enabled = true
		}
	}

	if d.attr.needEnabledMode && !enabled {
		enableErr := d.enable(logger, session, &capture)
		if enableErr != nil {
			resultCh <- FetchResult{Model: modelName, DevId: d.id, DevHostPort: d.hostPort, Transport: transport, Msg: fmt.Sprintf("fetch enable: %v", enableErr), Code: FETCH_ERR_ENABLE, Begin: begin}
			return
		}
	}

	if d.attr.needPagingOff {
		pagingErr := d.pagingOff(logger, session, &capture)
		if pagingErr != nil {
			resultCh <- FetchResult{Model: modelName, DevId: d.id, DevHostPort: d.hostPort, Transport: transport, Msg: fmt.Sprintf("fetch pager off: %v", pagingErr), Code: FETCH_ERR_PAGER, Begin: begin}
			return
		}
	}

	if cmdErr := d.sendCommands(logger, session, &capture); cmdErr != nil {
		d.saveRollback(logger, &capture)
		resultCh <- FetchResult{Model: modelName, DevId: d.id, DevHostPort: d.hostPort, Transport: transport, Msg: fmt.Sprintf("commands: %v", cmdErr), Code: FETCH_ERR_COMMANDS, Begin: begin}
		return
	}

	if saveErr := d.saveCommit(logger, &capture, repository, maxFiles); saveErr != nil {
		resultCh <- FetchResult{Model: modelName, DevId: d.id, DevHostPort: d.hostPort, Transport: transport, Msg: fmt.Sprintf("save commit: %v", saveErr), Code: FETCH_ERR_SAVE, Begin: begin}
		return
	}

	resultCh <- FetchResult{Model: modelName, DevId: d.id, DevHostPort: d.hostPort, Transport: transport, Code: FETCH_ERR_NONE, Begin: begin}
}

func (d *Device) saveRollback(logger hasPrintf, capture *dialog) {
	capture.save = nil
}

func (d *Device) DeviceDir(repository string) string {
	return filepath.Join(repository, d.id)
}

func (d *Device) DevicePathPrefix(devDir string) string {
	return filepath.Join(devDir, d.id) + "."
}

func (d *Device) saveCommit(logger hasPrintf, capture *dialog, repository string, maxFiles int) error {

	devDir := d.DeviceDir(repository)

	if mkdirErr := os.MkdirAll(devDir, 0700); mkdirErr != nil {
		return fmt.Errorf("saveCommit: mkdir: error: %v", mkdirErr)
	}

	devPathPrefix := d.DevicePathPrefix(devDir)

	// writeFunc: copy command outputs into file
	writeFunc := func(w conf.HasWrite) error {
		for _, b := range capture.save {
			n, writeErr := w.Write(b)
			if writeErr != nil {
				return fmt.Errorf("saveCommit: writeFunc: error: %v", writeErr)
			}
			if n != len(b) {
				return fmt.Errorf("saveCommit: writeFunc: partial: wrote=%d size=%d", n, len(b))
			}
		}
		return nil
	}

	path, writeErr := conf.SaveNewConfig(devPathPrefix, maxFiles, logger, writeFunc)
	if writeErr != nil {
		return fmt.Errorf("saveCommit: error: %v", writeErr)
	}

	logger.Printf("saveCommit: dev '%s' saved to '%s'", d.id, path)

	return nil
}

type hasTimeout interface {
	Timeout() bool
}

func (d *Device) match(logger hasPrintf, t transp, capture *dialog, patterns []string) (int, []byte, error) {

	const badIndex = -1
	var matchBuf []byte

	var expList []*regexp.Regexp

	// patterns[0] == "" --> look for EOF
	if patterns[0] != "" {
		expList = make([]*regexp.Regexp, len(patterns))
		for i, p := range patterns {
			exp, badExp := regexp.Compile(p)
			if badExp != nil {
				return badIndex, matchBuf, fmt.Errorf("match: bad pattern '%s': %v", p, badExp)
			}
			expList[i] = exp
		}
	}

	begin := time.Now()
	buf := make([]byte, 100000)

	for {
		now := time.Now()
		if now.Sub(begin) > d.attr.matchTimeout {
			return badIndex, matchBuf, fmt.Errorf("match: timed out: %s", d.attr.matchTimeout)
		}

		deadline := now.Add(d.attr.readTimeout)
		if err := t.SetDeadline(deadline); err != nil {
			return badIndex, matchBuf, fmt.Errorf("match: could not set read timeout: %v", err)
		}

		eof := false

		n, readErr := t.Read(buf)
		if readErr != nil {
			if te, ok := readErr.(hasTimeout); ok {
				if te.Timeout() {
					return badIndex, matchBuf, fmt.Errorf("match: read timed out (%s): %v", d.attr.readTimeout, readErr)
				}
			}
			switch {
			case readErr == io.EOF:
				eof = true // EOF is normal termination for SSH transport
			default:
				return badIndex, matchBuf, fmt.Errorf("match: unexpected error: %v", readErr)
			}
		}
		if n < 1 && !eof {
			return badIndex, matchBuf, fmt.Errorf("match: unexpected empty read")
		}

		lastRead := buf[:n]
		matchBuf = append(matchBuf, lastRead...)
		capture.record(lastRead) // record full capture (for debbugging, etc)

		//logger.Printf("match: debug: read=%d newsize=%d", n, len(capture.buf))

		lastLine := findLastLine(matchBuf)

		//logger.Printf("match: debug: lastLine[%s]", lastLine)

		if expList != nil {
			for i, exp := range expList {
				if exp.Match(lastLine) {
					return i, matchBuf, nil // pattern found
				}
			}
		}

		if eof {
			return badIndex, matchBuf, io.EOF
		}
	}
}

func findLastLine(buf []byte) []byte {

	// remove possible trailing CR LF from end of line
	if len(buf) > 0 && buf[len(buf)-1] == '\n' {
		// found LF
		buf = buf[:len(buf)-1] // drop LF
		if len(buf) > 0 && buf[len(buf)-1] == '\r' {
			// found CR
			buf = buf[:len(buf)-1] // drop CR
		}
	}

	lastEOL := bytes.LastIndexAny(buf, "\r\n")
	lineBegin := lastEOL + 1
	lastLine := buf[lineBegin:]

	return lastLine
}

func (d *Device) send(logger hasPrintf, t transp, msg string) error {

	deadline := time.Now().Add(d.attr.sendTimeout)
	if err := t.SetDeadline(deadline); err != nil {
		return fmt.Errorf("send: could not set read timeout: %v", err)
	}

	_, wrErr := t.Write([]byte(msg))

	return wrErr
}

func (d *Device) sendCommands(logger hasPrintf, t transp, capture *dialog) error {

	// save timeouts
	saveReadTimeout := d.attr.readTimeout
	saveMatchTimeout := d.attr.matchTimeout

	// temporarily change timeouts
	d.attr.readTimeout = d.attr.commandReadTimeout
	d.attr.matchTimeout = d.attr.commandMatchTimeout

	// restore timeouts
	defer func() {
		d.attr.readTimeout = saveReadTimeout
		d.attr.matchTimeout = saveMatchTimeout
	}()

	for i, c := range d.attr.commandList {

		if err := d.send(logger, t, c); err != nil {
			return fmt.Errorf("sendCommands: could not send command [%d] '%s': %v", i, c, err)
		}

		pattern := d.attr.enabledPromptPattern

		_, matchBuf, matchErr := d.match(logger, t, capture, []string{pattern})
		switch matchErr {
		case nil: // ok
		case io.EOF:
			if pattern != "" {
				return fmt.Errorf("sendCommands: EOF could not match command prompt: %v buf=[%s]", matchErr, matchBuf)
			}
			logger.Printf("sendCommands: found wanted EOF")
		default:
			return fmt.Errorf("sendCommands: could not match command prompt: %v buf=[%s]", matchErr, matchBuf)
		}

		if saveErr := d.save(logger, capture, c, matchBuf); saveErr != nil {
			return fmt.Errorf("sendCommands: could not save command '%s' result: %v", c, saveErr)
		}
	}

	return nil
}

func (d *Device) save(logger hasPrintf, capture *dialog, command string, buf []byte) error {
	capture.save = append(capture.save, []byte(command), buf)
	return nil
}

func (d *Device) pagingOff(logger hasPrintf, t transp, capture *dialog) error {
	if pagerErr := d.send(logger, t, d.attr.disablePagerCommand); pagerErr != nil {
		return fmt.Errorf("pager off: could not send pager disabling command '%s': %v", d.attr.disablePagerCommand, pagerErr)
	}

	if _, _, err := d.match(logger, t, capture, []string{d.attr.enabledPromptPattern}); err != nil {
		return fmt.Errorf("pager off: could not match command prompt: %v", err)
	}

	return nil
}

func (d *Device) enable(logger hasPrintf, t transp, capture *dialog) error {
	if enableErr := d.send(logger, t, d.attr.enableCommand); enableErr != nil {
		return fmt.Errorf("enable: could not send enable command '%s': %v", d.attr.enableCommand, enableErr)
	}

	m, _, err := d.match(logger, t, capture, []string{d.attr.enablePasswordPromptPattern, d.attr.enabledPromptPattern})
	if err != nil {
		return fmt.Errorf("enable: could not match after-enable prompt: %v", err)
	}

	if m == 1 {
		return nil // found enabled command prompt
	}

	if passErr := d.send(logger, t, d.enablePassword); passErr != nil {
		return fmt.Errorf("enable: could not send enable password: %v", passErr)
	}

	if _, _, mismatch := d.match(logger, t, capture, []string{d.attr.enabledPromptPattern}); mismatch != nil {
		return fmt.Errorf("enable: could not find enabled command prompt: %v", mismatch)
	}

	return nil
}

func (d *Device) login(logger hasPrintf, t transp, capture *dialog) (bool, error) {

	m1, _, err := d.match(logger, t, capture, []string{d.attr.usernamePromptPattern, d.attr.passwordPromptPattern})
	if err != nil {
		return false, fmt.Errorf("login: could not find username prompt: %v", err)
	}

	switch m1 {
	case 0:
		logger.Printf("login: found username prompt")

		if userErr := d.send(logger, t, d.loginUser); userErr != nil {
			return false, fmt.Errorf("login: could not send username: %v", userErr)
		}

		_, _, err := d.match(logger, t, capture, []string{d.attr.passwordPromptPattern})
		if err != nil {
			return false, fmt.Errorf("login: could not find password prompt: %v", err)
		}

	case 1:
		logger.Printf("login: found password prompt")
	}

	if passErr := d.send(logger, t, d.loginPassword); passErr != nil {
		return false, fmt.Errorf("login: could not send password: %v", passErr)
	}

	m, _, err := d.match(logger, t, capture, []string{d.attr.disabledPromptPattern, d.attr.enabledPromptPattern})
	if err != nil {
		return false, fmt.Errorf("login: could not find command prompt: %v", err)
	}

	switch m {
	case 0:
		logger.Printf("login: found disabled command prompt")
	case 1:
		logger.Printf("login: found enabled command prompt")
	}

	enabled := m == 1

	return enabled, nil
}

func round(val float64) int {
	if val < 0 {
		return int(val - 0.5)
	}
	return int(val + 0.5)
}

func ScanDevices(tab *DeviceTable, logger hasPrintf, maxConcurrency int, delayMin, delayMax time.Duration, repository string, maxFiles, holdtime int) (int, int, int) {

	devices := tab.ListDevices()
	deviceCount := len(devices)

	logger.Printf("ScanDevices: starting devices=%d maxConcurrency=%d", deviceCount, maxConcurrency)
	if deviceCount < 1 {
		logger.Printf("ScanDevices: aborting")
		return 0, 0, 0
	}

	begin := time.Now()
	random := rand.New(rand.NewSource(begin.UnixNano()))

	resultCh := make(chan FetchResult)

	logger.Printf("ScanDevices: per-device delay before starting: %d-%d ms", delayMin/time.Millisecond, delayMax/time.Millisecond)

	elapMax := 0 * time.Second
	elapMin := 24 * time.Hour
	wait := 0
	nextDevice := 0
	success := 0
	skipped := 0

	for nextDevice < deviceCount || wait > 0 {

		// launch additional devices
		for nextDevice < deviceCount {
			// there are devices to process

			if maxConcurrency > 0 && wait >= maxConcurrency {
				break // max concurrent limit reached
			}

			d := devices[nextDevice]

			if h := d.Holdtime(time.Now(), holdtime); h > 0 {

				// do not handle device yet (holdtime not expired)
				logger.Printf("device: %s skipping due to holdtime=%s", d.Id(), h)
				skipped++

			} else {

				// launch one additional per-device goroutine

				r := random.Float64()
				var delay time.Duration
				if delayMax > 0 {
					delay = time.Duration(round(r*float64(delayMax-delayMin))) + delayMin
				}
				go d.Fetch(logger, resultCh, delay, repository, maxFiles) // per-device goroutine
				wait++

			}

			nextDevice++
		}

		// wait for one device to finish
		r := <-resultCh
		wait--
		end := time.Now()
		elap := end.Sub(r.Begin)
		logger.Printf("device result: %s %s %s %s msg=[%s] code=%d wait=%d remain=%d skipped=%d elap=%s", r.Model, r.DevId, r.DevHostPort, r.Transport, r.Msg, r.Code, wait, deviceCount-nextDevice, skipped, elap)

		good := r.Code == FETCH_ERR_NONE
		updateDeviceStatus(tab, r.DevId, good, end, logger)

		if good {
			success++
		}
		if elap < elapMin {
			elapMin = elap
		}
		if elap > elapMax {
			elapMax = elap
		}
	}

	elapsed := time.Since(begin)
	average := elapsed / time.Duration(deviceCount)

	logger.Printf("ScanDevices: finished elapsed=%s devices=%d success=%d skipped=%d average=%s min=%s max=%s", elapsed, deviceCount, success, skipped, average, elapMin, elapMax)

	return success, deviceCount - success, skipped
}

func updateDeviceStatus(tab *DeviceTable, devId string, good bool, last time.Time, logger hasPrintf) {
	d, getErr := tab.GetDevice(devId)
	if getErr != nil {
		logger.Printf("updateDeviceStatus: '%s' not found: %v", getErr)
		return
	}

	d.lastTry = last
	d.lastStatus = good
	if d.lastStatus {
		d.lastSuccess = d.lastTry
	}

	tab.UpdateDevice(d)
}

func UpdateLastSuccess(tab *DeviceTable, logger hasPrintf, repository string) {
	for _, d := range tab.ListDevices() {
		prefix := d.DevicePathPrefix(d.DeviceDir(repository))

		lastConfig, lastErr := conf.FindLastConfig(prefix, logger)
		if lastErr != nil {
			logger.Printf("UpdateLastSuccess: find last: '%s': %v", prefix, lastErr)
			continue
		}

		f, openErr := os.Open(lastConfig)
		if openErr != nil {
			logger.Printf("UpdateLastSuccess: open: '%s': %v", lastConfig, openErr)
			continue
		}

		info, statErr := f.Stat()
		if statErr != nil {
			logger.Printf("UpdateLastSuccess: stat: '%s': %v", lastConfig, statErr)
		} else {
			d.lastSuccess = info.ModTime()
			tab.UpdateDevice(d)
		}

		if closeErr := f.Close(); closeErr != nil {
			logger.Printf("UpdateLastSuccess: close: '%s': %v", lastConfig, closeErr)
		}

	}
}
