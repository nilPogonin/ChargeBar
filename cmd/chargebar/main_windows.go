// ChargeBar is a native Windows tray application with no third-party dependencies.
package main

import (
	"chargebar/internal/battery"
	"chargebar/internal/native"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const version = "1.0.0"
const wmTray = 0x8001
const wmResult = 0x8002
const wmReveal = 0x8003
const wmDiagnose = 0x8004
const className = "ChargeBar.Window.v1"
const miniClass = "ChargeBar.Mini.v1"

type settings struct {
	Widget          bool
	Notifications   bool
	Threshold, Poll int
	Moved           bool
	X, Y            int32
}

var config = settings{Notifications: true, Threshold: 20, Poll: 30}
var configDir string
var hwnd, mini uintptr
var dpi = 96
var taskbarMessage uint32
var demo bool
var shown bool
var icons [2]uintptr
var iconAdded [2]bool
var samples = [2]battery.Sample{battery.Empty(battery.Mouse, "Поиск устройства…"), battery.Empty(battery.Headset, "Поиск устройства…")}
var alerts [2]battery.Alert
var latest native.Snapshot
var pollNow = make(chan struct{}, 1)
var results = make(chan native.Snapshot, 1)
var stopping = make(chan struct{})
var callback = syscall.NewCallback(windowProc)
var signature string
var diagnosticsPending bool
var firstRun bool
var pendingAlerts [2]bool

func scale(v int) int { return v * dpi / 96 }
func bounds(x, y, w, h int) rect {
	return rect{int32(scale(x)), int32(scale(y)), int32(scale(x + w)), int32(scale(y + h))}
}
func requestPoll() {
	select {
	case pollNow <- struct{}{}:
	default:
	}
}
func saveConfig() {
	if demo {
		return
	}
	data, e := json.MarshalIndent(config, "", "  ")
	if e == nil {
		e = os.WriteFile(filepath.Join(configDir, "settings.json"), data, 0600)
	}
	if e != nil {
		info("ChargeBar", "Не удалось сохранить настройки: "+e.Error())
	}
}
func loadConfig() {
	root := os.Getenv("APPDATA")
	if root == "" {
		root = os.TempDir()
	}
	configDir = filepath.Join(root, "ChargeBar")
	os.MkdirAll(configDir, 0700)
	if data, e := os.ReadFile(filepath.Join(configDir, "settings.json")); e == nil {
		json.Unmarshal(data, &config)
	} else if os.IsNotExist(e) {
		firstRun = true
	}
	if config.Threshold != 15 && config.Threshold != 20 && config.Threshold != 30 {
		config.Threshold = 20
	}
	if config.Poll != 15 && config.Poll != 30 && config.Poll != 60 {
		config.Poll = 30
	}
}
func isStartup() bool {
	var key syscall.Handle
	if syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, ptr(`Software\Microsoft\Windows\CurrentVersion\Run`), 0, 0x20019, &key) != nil {
		return false
	}
	defer syscall.RegCloseKey(key)
	var n uint32
	return syscall.RegQueryValueEx(key, ptr("ChargeBar"), nil, nil, nil, &n) == nil && n > 2
}
func startup(on bool) error {
	if demo {
		return fmt.Errorf("автозапуск недоступен в деморежиме")
	}
	var key syscall.Handle
	err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, ptr(`Software\Microsoft\Windows\CurrentVersion\Run`), 0, 0x20006, &key)
	if err == syscall.ERROR_FILE_NOT_FOUND {
		r, _, _ := advapi.NewProc("RegCreateKeyExW").Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(ptr(`Software\Microsoft\Windows\CurrentVersion\Run`))), 0, 0, 0, 0x20006, 0, uintptr(unsafe.Pointer(&key)), 0)
		if r == 0 {
			err = nil
		} else {
			err = syscall.Errno(r)
		}
	}
	if err != nil {
		return err
	}
	defer syscall.RegCloseKey(key)
	if !on {
		r, _, _ := advapi.NewProc("RegDeleteValueW").Call(uintptr(key), uintptr(unsafe.Pointer(ptr("ChargeBar"))))
		if r != 0 && r != 2 {
			return syscall.Errno(r)
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	value := syscall.StringToUTF16(`"` + exe + `"`)
	r, _, _ := advapi.NewProc("RegSetValueExW").Call(uintptr(key), uintptr(unsafe.Pointer(ptr("ChargeBar"))), 0, 1, uintptr(unsafe.Pointer(&value[0])), uintptr(len(value)*2))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func main() {
	runtime.LockOSThread()
	for _, a := range os.Args[1:] {
		if a == "--demo" {
			demo = true
		}
	}
	loadConfig()
	diagnose := false
	for _, a := range os.Args[1:] {
		if a == "--diagnose" {
			diagnose = true
		}
	}
	mutexName := `Local\ChargeBar.v1`
	if demo {
		mutexName += ".demo"
	}
	mutex, _, err := kernel.NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(ptr(mutexName))))
	if err == syscall.ERROR_ALREADY_EXISTS {
		title := "ChargeBar"
		if demo {
			title = "ChargeBar Demo"
		}
		existing, _, _ := findWindow.Call(uintptr(unsafe.Pointer(ptr(className))), uintptr(unsafe.Pointer(ptr(title))))
		if existing != 0 {
			msg := uintptr(wmReveal)
			if diagnose {
				msg = wmDiagnose
			}
			postMessage.Call(existing, msg, 0, 0)
		}
		return
	}
	if mutex != 0 {
		defer syscall.CloseHandle(syscall.Handle(mutex))
	}
	if diagnose {
		if demo {
			info("ChargeBar", "Диагностика недоступна в деморежиме.")
			return
		}
		path, e := writeDiagnostics(native.Poll())
		if e != nil {
			info("ChargeBar", e.Error())
		} else {
			openFile(path)
		}
		return
	}
	if setDPI.Find() == nil {
		setDPI.Call(^uintptr(3))
	}
	instance, _, _ := kernel.NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := loadCursor.Call(0, 32512)
	for _, name := range []string{className, miniClass} {
		wc := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Proc: callback, Instance: instance, Cursor: cursor, Name: ptr(name)}
		if v, _, _ := registerClass.Call(uintptr(unsafe.Pointer(&wc))); v == 0 {
			info("ChargeBar", "Не удалось зарегистрировать окно")
			return
		}
	}
	title := "ChargeBar"
	if demo {
		title = "ChargeBar Demo"
	}
	hwnd, _, _ = createWindow.Call(0x80, uintptr(unsafe.Pointer(ptr(className))), uintptr(unsafe.Pointer(ptr(title))), 0x80000000, 0, 0, 380, 310, 0, 0, instance, 0)
	if hwnd == 0 {
		info("ChargeBar", "Не удалось создать окно")
		return
	}
	if getDPI.Find() == nil {
		if v, _, _ := getDPI.Call(hwnd); v > 0 {
			dpi = int(v)
		}
	}
	mini, _, _ = createWindow.Call(0x08000088, uintptr(unsafe.Pointer(ptr(miniClass))), uintptr(unsafe.Pointer(ptr("ChargeBar Compact"))), 0x80000000, 0, 0, u(scale(244)), u(scale(46)), 0, 0, instance, 0)
	for _, w := range []uintptr{hwnd, mini} {
		if w != 0 {
			dark := int32(1)
			setAttribute.Call(w, 20, uintptr(unsafe.Pointer(&dark)), 4)
			corner := int32(2)
			setAttribute.Call(w, 33, uintptr(unsafe.Pointer(&corner)), 4)
		}
	}
	t, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(ptr("TaskbarCreated"))))
	taskbarMessage = uint32(t)
	updateTray(true)
	positionPanel()
	positionMini()
	setTimer.Call(hwnd, 1, 2000, 0)
	if demo || firstRun {
		showPanel()
		if firstRun {
			saveConfig()
		}
	}
	go worker()
	requestPoll()
	nextPoll = time.Now().Add(time.Duration(config.Poll) * time.Second)
	var m message
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	close(stopping)
	for i := range icons {
		if iconAdded[i] {
			n := notifyData{Size: uint32(unsafe.Sizeof(notifyData{})), Window: hwnd, ID: uint32(i + 1)}
			notify.Call(2, uintptr(unsafe.Pointer(&n)))
		}
		if icons[i] != 0 {
			destroyIcon.Call(icons[i])
		}
	}
}

func worker() {
	for {
		select {
		case <-stopping:
			return
		case <-pollNow:
		}
		var s native.Snapshot
		if demo {
			s = native.Snapshot{At: time.Now(), Samples: [2]battery.Sample{{Kind: battery.Mouse, Percent: 83, Connection: "2,4 ГГц", At: time.Now()}, {Kind: battery.Headset, Percent: 36, Connection: "2,4 ГГц", At: time.Now()}}, Present: [2]bool{true, true}}
		} else {
			s = native.Poll()
		}
		select {
		case results <- s:
			postMessage.Call(hwnd, wmResult, 0, 0)
		case <-stopping:
			return
		}
	}
}

var nextPoll time.Time

func acceptResult() {
	select {
	case snap := <-results:
		latest = snap
		for i, s := range snap.Samples {
			samples[i] = battery.Merge(samples[i], s, time.Now(), snap.Present[i])
			if alerts[i].Update(samples[i], config.Threshold) && config.Notifications && !demo {
				pendingAlerts[i] = true
			}
		}
		flushAlerts()
		updateTray(false)
		invalidate.Call(hwnd, 0, 0)
		invalidate.Call(mini, 0, 0)
		if diagnosticsPending {
			diagnosticsPending = false
			path, e := writeDiagnostics(snap)
			if e != nil {
				info("Диагностика", e.Error())
			} else {
				openFile(path)
			}
		}
	default:
	}
}

func windowProc(w uintptr, msg uint32, wp, lp uintptr) uintptr {
	if taskbarMessage != 0 && msg == taskbarMessage {
		iconAdded = [2]bool{}
		updateTray(true)
		return 0
	}
	switch msg {
	case 0x0014:
		return 1 // WM_ERASEBKGND; WM_PAINT paints the full surface.
	case 0x000f:
		paint(w)
		return 0
	case wmResult:
		acceptResult()
		return 0
	case wmReveal:
		showPanel()
		return 0
	case wmDiagnose:
		if !demo {
			diagnosticsPending = true
			requestPoll()
		}
		return 0
	case 0x0113:
		if wp == 2 {
			user.NewProc("KillTimer").Call(hwnd, 2)
			requestPoll()
			return 0
		}
		if time.Now().After(nextPoll) {
			requestPoll()
			nextPoll = time.Now().Add(time.Duration(config.Poll) * time.Second)
		}
		if !iconAdded[0] || !iconAdded[1] {
			updateTray(true)
		}
		flushAlerts()
		if config.Widget {
			if fullScreen() {
				showWindow.Call(mini, 0)
			} else {
				showWindow.Call(mini, 4)
			}
		}
		return 0
	case 0x0219:
		requestPoll()
		setTimer.Call(hwnd, 2, 1000, 0)
		return 1
	case 0x0218:
		if wp == 0x12 || wp == 7 {
			requestPoll()
		}
		return 1
	case 0x001a, 0x007e:
		positionMini()
		if shown {
			positionPanel()
		}
		return 0
	case 0x02e0:
		dpi = int(wp & 0xffff)
		positionMini()
		if shown {
			positionPanel()
		}
		updateTray(true)
		return 0
	case wmTray:
		switch uint32(lp) {
		case 0x0202, 0x0400:
			if shown {
				hidePanel()
			} else {
				showPanel()
			}
		case 0x0205, 0x007b:
			menu()
		case 0x0401:
			menu()
		}
		return 0
	case 0x0205:
		menu()
		return 0
	case 0x0201:
		if w == mini {
			releaseCapture.Call()
			sendMessage.Call(w, 0x00a1, 2, 0)
			return 0
		}
		y := int(int16((lp>>16)&0xffff)) * 96 / dpi
		x := int(int16(lp&0xffff)) * 96 / dpi
		if y >= 260 {
			if x < 240 {
				requestPoll()
			} else {
				menu()
			}
		}
		return 0
	case 0x0232:
		if w == mini {
			var r rect
			getWindowRect.Call(w, uintptr(unsafe.Pointer(&r)))
			config.X = r.L
			config.Y = r.T
			config.Moved = true
			saveConfig()
		}
		return 0
	case 0x0006:
		if w == hwnd && wp&0xffff == 0 {
			hidePanel()
		}
		return 0
	case 0x0010:
		if w == hwnd {
			hidePanel()
		}
		return 0
	case 0x0100:
		if wp == 27 {
			hidePanel()
			return 0
		}
	case 0x0002:
		postQuit.Call(0)
		return 0
	}
	r, _, _ := defWindowProc.Call(w, uintptr(msg), wp, lp)
	return r
}

func workArea() (rect, rect) {
	task, _, _ := findWindow.Call(uintptr(unsafe.Pointer(ptr("Shell_TrayWnd"))), 0)
	mon, _, _ := monitorFromWindow.Call(task, 1)
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	getMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
	if mi.Work.R == 0 {
		mi.Work = rect{0, 0, 1920, 1040}
		mi.Monitor = rect{0, 0, 1920, 1080}
	}
	return mi.Work, mi.Monitor
}
func positionPanel() {
	r, _ := workArea()
	setWindowPos.Call(hwnd, ^uintptr(0), u(int(r.R)-scale(392)), u(int(r.B)-scale(312)), u(scale(380)), u(scale(300)), 0x10)
}
func positionMini() {
	r, _ := workArea()
	x := int(r.R) - scale(256)
	y := int(r.B) - scale(56)
	if config.Moved {
		x = int(config.X)
		y = int(config.Y)
	}
	// Clamp after display changes so the widget cannot disappear off the primary screen.
	x = max(int(r.L), min(x, int(r.R)-scale(244)))
	y = max(int(r.T), min(y, int(r.B)-scale(46)))
	setWindowPos.Call(mini, ^uintptr(0), u(x), u(y), u(scale(244)), u(scale(46)), 0x10)
	if config.Widget && !fullScreen() {
		showWindow.Call(mini, 4)
	} else {
		showWindow.Call(mini, 0)
	}
}
func fullScreen() bool {
	f, _, _ := getForeground.Call()
	if f == 0 || f == hwnd || f == mini {
		return false
	}
	// Desktop and taskbar are not fullscreen apps.
	for _, cls := range []string{"Progman", "WorkerW", "Shell_TrayWnd"} {
		h, _, _ := findWindow.Call(uintptr(unsafe.Pointer(ptr(cls))), 0)
		if f == h {
			return false
		}
	}
	var r rect
	getWindowRect.Call(f, uintptr(unsafe.Pointer(&r)))
	_, m := workArea()
	return r.L <= m.L && r.T <= m.T && r.R >= m.R && r.B >= m.B
}
func showPanel() {
	positionPanel()
	showWindow.Call(hwnd, 5)
	foreground.Call(hwnd)
	shown = true
	requestPoll()
}
func hidePanel() { showWindow.Call(hwnd, 0); shown = false }

func sampleColor(s battery.Sample) uintptr {
	if s.Percent < 0 || s.Stale {
		return color(0x84919f)
	}
	if s.Charging {
		return color(0x71baff)
	}
	if s.Percent <= config.Threshold {
		return color(0xff757d)
	}
	if s.Kind == battery.Headset {
		return color(0xbfa6ff)
	}
	return color(0x75e0b3)
}
func paint(w uintptr) {
	var ps paintStruct
	dc, _, _ := beginPaint.Call(w, uintptr(unsafe.Pointer(&ps)))
	defer endPaint.Call(w, uintptr(unsafe.Pointer(&ps)))
	width, height := 380, 300
	if w == mini {
		width, height = 244, 46
	}
	background := color(0x141920)
	b, _, _ := createBrush.Call(background)
	r := bounds(0, 0, width, height)
	fillRect.Call(dc, uintptr(unsafe.Pointer(&r)), b)
	deleteObject.Call(b)
	if w == mini {
		for i, s := range samples {
			x := 12 + i*122
			pictogram(dc, i, scale(x), scale(13), scale(20), sampleColor(s))
			text := s.Value()
			if s.Charging {
				text += " ↯"
			}
			callText(dc, text, bounds(x+30, 6, 78, 34), scale(19), 600, color(0xe9f0f7), 0x04)
		}
		return
	}
	title := "ChargeBar"
	if demo {
		title += " · ДЕМО"
	}
	callText(dc, title, bounds(22, 14, 250, 30), scale(22), 600, color(0xf2f6fa), 0x04)
	callText(dc, "Заряд под рукой", bounds(22, 46, 260, 22), scale(12), 400, color(0x8f9dab), 0x04)
	for i, s := range samples {
		y := 82 + i*82
		c := sampleColor(s)
		box(dc, bounds(14, y, 352, 74), scale(16), color(0x1f2731))
		pictogram(dc, i, scale(28), scale(y+22), scale(25), c)
		callText(dc, battery.Name(s.Kind), bounds(68, y+11, 180, 25), scale(15), 600, color(0xe8eff5), 0x04)
		status := s.Status()
		if demo {
			status = "Демонстрационные данные"
		}
		callText(dc, status, bounds(68, y+37, 210, 20), scale(11), 400, color(0x9aa8b6), 0x04)
		callText(dc, s.Value(), bounds(268, y+8, 82, 29), scale(23), 600, c, 0x02|0x04)
		box(dc, bounds(280, y+47, 68, 4), scale(4), color(0x35404d))
		if s.Percent > 0 {
			box(dc, bounds(280, y+47, max(2, 68*s.Percent/100), 4), scale(4), c)
		}
	}
	callText(dc, "↻  Обновить", bounds(22, 262, 160, 25), scale(13), 500, color(0x75e0b3), 0x04)
	callText(dc, "Настройки  ···", bounds(242, 262, 116, 25), scale(13), 400, color(0xa7b5c3), 0x02|0x04)
}

func makeIcon(s battery.Sample) uintptr {
	size := scale(16)
	if size < 16 {
		size = 16
	}
	if size > 48 {
		size = 48
	}
	dc, _, _ := getDC.Call(0)
	mem, _, _ := createDC.Call(dc)
	releaseDC.Call(0, dc)
	defer deleteDC.Call(mem)
	bi := bitmapInfo{Header: bitmapHeader{Size: 40, Width: int32(size), Height: -int32(size), Planes: 1, BitCount: 32}}
	var bits unsafe.Pointer
	bitmap, _, _ := createDIB.Call(mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return 0
	}
	defer deleteObject.Call(bitmap)
	old, _, _ := selectObject.Call(mem, bitmap)
	pixels := unsafe.Slice((*byte)(bits), size*size*4)
	clear(pixels)
	str := "—"
	if s.Percent >= 0 {
		str = fmt.Sprint(s.Percent)
	}
	fontSize := size * 86 / 100
	if s.Percent == 100 {
		fontSize = size * 70 / 100
	}
	callText(mem, str, rect{0, -1, int32(size), int32(size - 2)}, fontSize, 700, sampleColor(s), 0x01|0x04)
	// A narrow underline distinguishes the two devices even when percentages match.
	br, _, _ := createBrush.Call(sampleColor(s))
	bar := rect{0, int32(size - 2), int32(size), int32(size)}
	if s.Kind == battery.Headset {
		bar.L = int32(size / 3)
		bar.R = int32(size * 2 / 3)
	}
	fillRect.Call(mem, uintptr(unsafe.Pointer(&bar)), br)
	deleteObject.Call(br)
	gdi.NewProc("GdiFlush").Call()
	for i := 0; i < len(pixels); i += 4 {
		pixels[i+3] = max(pixels[i], pixels[i+1], pixels[i+2])
	}
	selectObject.Call(mem, old)
	// A zero AND mask; the 32-bit color bitmap carries alpha.
	maskBytes := make([]byte, ((size+15)/16)*2*size)
	mask, _, _ := createBitmap.Call(u(size), u(size), 1, 1, uintptr(unsafe.Pointer(&maskBytes[0])))
	defer deleteObject.Call(mask)
	ii := iconInfo{IsIcon: 1, Mask: mask, Color: bitmap}
	icon, _, _ := createIcon.Call(uintptr(unsafe.Pointer(&ii)))
	return icon
}
func updateTray(force bool) {
	sig := fmt.Sprintf("%v/%d/%d", samples, config.Threshold, dpi)
	if !force && signature == sig {
		return
	}
	signature = sig
	for i, s := range samples {
		icon := makeIcon(s)
		n := notifyData{Size: uint32(unsafe.Sizeof(notifyData{})), Window: hwnd, ID: uint32(i + 1), Flags: 1 | 2 | 4, Callback: wmTray, Icon: icon}
		tip := battery.Name(s.Kind) + " · " + s.Value() + "\n" + s.Status()
		if demo {
			tip = "ДЕМО · " + tip
		}
		copyText(n.Tip[:], tip)
		action := uintptr(1)
		if !iconAdded[i] {
			action = 0
		}
		r, _, _ := notify.Call(action, uintptr(unsafe.Pointer(&n)))
		if r == 0 && action == 1 {
			r, _, _ = notify.Call(0, uintptr(unsafe.Pointer(&n)))
		}
		iconAdded[i] = r != 0
		if icons[i] != 0 {
			destroyIcon.Call(icons[i])
		}
		icons[i] = icon
	}
}
func balloon(index int, title, body string) {
	n := notifyData{Size: uint32(unsafe.Sizeof(notifyData{})), Window: hwnd, ID: uint32(index + 1), Flags: 0x10, InfoFlags: 1 | 0x10 | 0x80}
	copyText(n.InfoTitle[:], title)
	copyText(n.Info[:], body)
	notify.Call(1, uintptr(unsafe.Pointer(&n)))
}

func flushAlerts() {
	if fullScreen() {
		return
	}
	for i, p := range pendingAlerts {
		if !p {
			continue
		}
		pendingAlerts[i] = false
		s := samples[i]
		if config.Notifications && !demo && s.Percent >= 0 && s.Percent <= config.Threshold && !s.Stale && !s.Charging && !s.Wired {
			balloon(i, "Пора зарядить", fmt.Sprintf("%s: осталось %d%%", battery.Name(s.Kind), s.Percent))
		}
	}
}

func menu() {
	m, _, _ := createPopup.Call()
	defer destroyMenu.Call(m)
	item := func(id int, label string, checked bool) {
		flags := uintptr(0)
		if checked {
			flags = 8
		}
		appendMenu.Call(m, flags, u(id), uintptr(unsafe.Pointer(ptr(label))))
	}
	sep := func() { appendMenu.Call(m, 0x800, 0, 0) }
	item(1, "Открыть ChargeBar", false)
	item(2, "Обновить сейчас", false)
	sep()
	item(3, "Компактный виджет над панелью задач", config.Widget)
	item(4, "Вернуть виджет к часам", false)
	item(5, "Запускать с Windows", isStartup())
	item(6, "Уведомлять о низком заряде", config.Notifications)
	sep()
	for i, v := range []int{15, 20, 30} {
		item(10+i, fmt.Sprintf("Низкий заряд: %d%%", v), config.Threshold == v)
	}
	sep()
	for i, v := range []int{15, 30, 60} {
		item(20+i, fmt.Sprintf("Обновлять раз в %d сек.", v), config.Poll == v)
	}
	sep()
	item(30, "Сохранить диагностику…", false)
	item(31, "О программе", false)
	item(99, "Выход", false)
	var pos point
	getCursorPos.Call(uintptr(unsafe.Pointer(&pos)))
	foreground.Call(hwnd)
	cmd, _, _ := trackMenu.Call(m, 0x100|0x02, u(int(pos.X)), u(int(pos.Y)), 0, hwnd, 0)
	postMessage.Call(hwnd, 0, 0, 0)
	switch cmd {
	case 1:
		showPanel()
	case 2:
		requestPoll()
	case 3:
		config.Widget = !config.Widget
		saveConfig()
		positionMini()
	case 4:
		config.Moved = false
		config.Widget = true
		saveConfig()
		positionMini()
	case 5:
		if e := startup(!isStartup()); e != nil {
			info("Автозапуск", e.Error())
		}
	case 6:
		config.Notifications = !config.Notifications
		saveConfig()
	case 10, 11, 12:
		config.Threshold = []int{15, 20, 30}[int(cmd)-10]
		saveConfig()
		updateTray(true)
		invalidate.Call(hwnd, 0, 0)
	case 20, 21, 22:
		config.Poll = []int{15, 30, 60}[int(cmd)-20]
		saveConfig()
		nextPoll = time.Time{}
		requestPoll()
	case 30:
		if demo {
			info("ChargeBar", "Деморежим не обращается к устройствам.")
			break
		}
		diagnosticsPending = true
		requestPoll()
	case 31:
		info("ChargeBar "+version, "Заряд ATK Phantom и MCHOSE V9 Pro в трее.\n\nМышь — длинная линия под числом.\nНаушники — короткая линия.\nСиний — зарядка, красный — низкий заряд.\n~ и серый — последнее значение спящей мыши.\n\nРаботает локально. Параметры устройств не меняет.\nПроверка на ваших устройствах ещё необходима.")
	case 99:
		postQuit.Call(0)
	}
}

func writeDiagnostics(s native.Snapshot) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "ChargeBar %s | Windows %s\r\nGenerated: %s\r\nSample time: %s\r\n\r\n", version, runtime.GOARCH, time.Now().Format(time.RFC3339), s.At.Format(time.RFC3339))
	b.WriteString("Contains USB metadata and battery packets only; no serial numbers or device paths.\r\n\r\n")
	if s.Error != "" {
		fmt.Fprintf(&b, "Enumeration error: %s\r\n", s.Error)
	}
	for _, d := range s.Devices {
		p, w := battery.Classify(d)
		fmt.Fprintf(&b, "%04X:%04X | %s | page=%04X usage=%04X in=%d out=%d feature=%d | protocol=%d wired=%t\r\n", d.VID, d.PID, d.Product, d.Page, d.Usage, d.Input, d.Output, d.Feature, p, w)
	}
	if len(s.Devices) == 0 {
		b.WriteString("No ATK/MCHOSE HID interfaces found.\r\n")
	}
	for i, r := range s.Probes {
		fmt.Fprintf(&b, "\r\nProbe %d | %04X:%04X page=%04X usage=%04X protocol=%d\r\nTX %s\r\nRX %s\r\nError: %s\r\n", i+1, r.Device.VID, r.Device.PID, r.Device.Page, r.Device.Usage, r.Protocol, r.Request, r.Reply, r.Error)
	}
	for _, v := range s.Samples {
		fmt.Fprintf(&b, "\r\n%s: %s | %s\r\n", battery.Name(v.Kind), v.Value(), v.Status())
	}
	path := filepath.Join(configDir, "diagnostics-"+time.Now().Format("20060102-150405")+".txt")
	return path, os.WriteFile(path, []byte("\xef\xbb\xbf"+b.String()), 0600)
}
