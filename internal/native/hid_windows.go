package native

import (
	"chargebar/internal/battery"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var hid = syscall.NewLazyDLL("hid.dll")
var setup = syscall.NewLazyDLL("setupapi.dll")
var kernel = syscall.NewLazyDLL("kernel32.dll")
var getGuid = hid.NewProc("HidD_GetHidGuid")
var getAttr = hid.NewProc("HidD_GetAttributes")
var getProduct = hid.NewProc("HidD_GetProductString")
var getPreparsed = hid.NewProc("HidD_GetPreparsedData")
var freePreparsed = hid.NewProc("HidD_FreePreparsedData")
var getCaps = hid.NewProc("HidP_GetCaps")
var getDevs = setup.NewProc("SetupDiGetClassDevsW")
var enumInterface = setup.NewProc("SetupDiEnumDeviceInterfaces")
var getDetail = setup.NewProc("SetupDiGetDeviceInterfaceDetailW")
var destroyDevs = setup.NewProc("SetupDiDestroyDeviceInfoList")
var overlappedResult = kernel.NewProc("GetOverlappedResult")
var createEvent = kernel.NewProc("CreateEventW")

type interfaceData struct {
	Size     uint32
	Guid     syscall.GUID
	Flags    uint32
	Reserved uintptr
}
type attributes struct {
	Size              uint32
	VID, PID, Version uint16
}
type caps struct {
	Usage, Page, Input, Output, Feature                                                                                                       uint16
	Reserved                                                                                                                                  [17]uint16
	Links, InputButtons, InputValues, InputIndices, OutputButtons, OutputValues, OutputIndices, FeatureButtons, FeatureValues, FeatureIndices uint16
}

// ABI checks on both Windows x64 and arm64.
var _ [32 - unsafe.Sizeof(interfaceData{})]byte
var _ [unsafe.Sizeof(interfaceData{}) - 32]byte
var _ [64 - unsafe.Sizeof(caps{})]byte
var _ [unsafe.Sizeof(caps{}) - 64]byte

func Enumerate() ([]battery.Device, error) {
	var guid syscall.GUID
	getGuid.Call(uintptr(unsafe.Pointer(&guid)))
	set, _, e := getDevs.Call(uintptr(unsafe.Pointer(&guid)), 0, 0, 0x12)
	if set == ^uintptr(0) {
		return nil, fmt.Errorf("SetupDiGetClassDevs: %v", e)
	}
	defer destroyDevs.Call(set)
	var devices []battery.Device
	for i := uint32(0); ; i++ {
		data := interfaceData{Size: uint32(unsafe.Sizeof(interfaceData{}))}
		ok, _, err := enumInterface.Call(set, 0, uintptr(unsafe.Pointer(&guid)), uintptr(i), uintptr(unsafe.Pointer(&data)))
		if ok == 0 {
			if err == syscall.Errno(259) {
				break
			}
			return devices, fmt.Errorf("HID enumeration: %v", err)
		}
		var need uint32
		getDetail.Call(set, uintptr(unsafe.Pointer(&data)), 0, 0, uintptr(unsafe.Pointer(&need)), 0)
		if need < 8 || need > 65536 {
			continue
		}
		buf := make([]byte, need)
		binary.LittleEndian.PutUint32(buf, 8)
		ok, _, _ = getDetail.Call(set, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&buf[0])), uintptr(need), 0, 0)
		if ok == 0 {
			continue
		}
		chars := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[4])), (len(buf)-4)/2)
		path := syscall.UTF16ToString(chars)
		// Only inspect metadata of relevant vendors, never keyboard/mouse input collections.
		lower := strings.ToLower(path)
		if !strings.Contains(lower, "vid_373b") && !strings.Contains(lower, "vid_291d") {
			continue
		}
		p, _ := syscall.UTF16PtrFromString(path)
		h, err := syscall.CreateFile(p, 0, 3, nil, 3, 0, 0)
		if err != nil {
			continue
		}
		a := attributes{Size: uint32(unsafe.Sizeof(attributes{}))}
		getAttr.Call(uintptr(h), uintptr(unsafe.Pointer(&a)))
		c := caps{}
		var prep uintptr
		if ok, _, _ := getPreparsed.Call(uintptr(h), uintptr(unsafe.Pointer(&prep))); ok != 0 {
			getCaps.Call(prep, uintptr(unsafe.Pointer(&c)))
			freePreparsed.Call(prep)
		}
		var product [128]uint16
		getProduct.Call(uintptr(h), uintptr(unsafe.Pointer(&product[0])), uintptr(unsafe.Sizeof(product)))
		syscall.CloseHandle(h)
		devices = append(devices, battery.Device{Path: path, Product: syscall.UTF16ToString(product[:]), VID: a.VID, PID: a.PID, Page: c.Page, Usage: c.Usage, Input: c.Input, Output: c.Output, Feature: c.Feature})
	}
	return devices, nil
}

// Async I/O with cancellation. Buffers stay pinned until Windows acknowledges completion.
func ioReport(h syscall.Handle, b []byte, writing bool, deadline time.Time) ([]byte, error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, errors.New("тайм-аут")
	}
	ev, _, err := createEvent.Call(0, 1, 0, 0)
	if ev == 0 {
		return nil, err
	}
	defer syscall.CloseHandle(syscall.Handle(ev))
	ov := &syscall.Overlapped{HEvent: syscall.Handle(ev)}
	var pin runtime.Pinner
	pin.Pin(&b[0])
	pin.Pin(ov)
	defer pin.Unpin()
	var n uint32
	if writing {
		err = syscall.WriteFile(h, b, &n, ov)
	} else {
		err = syscall.ReadFile(h, b, &n, ov)
	}
	if err != nil && err != syscall.ERROR_IO_PENDING {
		return nil, err
	}
	if err == syscall.ERROR_IO_PENDING {
		ms := uint32(max(1, time.Until(deadline).Milliseconds()))
		result, waitErr := syscall.WaitForSingleObject(syscall.Handle(ev), ms)
		if result != 0 || waitErr != nil {
			syscall.CancelIoEx(h, ov)
			overlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(ov)), uintptr(unsafe.Pointer(&n)), 1)
			return nil, errors.New("тайм-аут чтения HID")
		}
		ok, _, e := overlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(ov)), uintptr(unsafe.Pointer(&n)), 0)
		if ok == 0 {
			return nil, e
		}
	}
	if int(n) > len(b) {
		return nil, errors.New("неверная длина ответа HID")
	}
	if writing && int(n) != len(b) {
		return nil, errors.New("запрос передан не полностью")
	}
	return b[:n], nil
}

type Probe struct {
	Device         battery.Device
	Protocol       battery.Protocol
	Request, Reply string
	Error          string
}
type Snapshot struct {
	Samples [2]battery.Sample
	Present [2]bool
	Devices []battery.Device
	Probes  []Probe
	Error   string
	At      time.Time
}

func exchange(h syscall.Handle, d battery.Device, p battery.Protocol, wired bool, cmd byte) (battery.Sample, Probe, error) {
	req := battery.Request(p, wired, cmd)
	pr := Probe{Device: d, Protocol: p, Request: fmt.Sprintf("% X", req)}
	frame := make([]byte, int(d.Output))
	copy(frame, req)
	deadline := time.Now().Add(900 * time.Millisecond)
	_, err := ioReport(h, frame, true, deadline)
	if err == nil {
		for attempt := 0; attempt < 12 && time.Now().Before(deadline); attempt++ {
			var reply []byte
			reply, err = ioReport(h, make([]byte, int(d.Input)), false, deadline)
			if err != nil {
				break
			}
			pr.Reply = fmt.Sprintf("% X", reply)
			if s, ok := battery.Parse(p, reply, wired, cmd); ok {
				return s, pr, nil
			}
		}
		if err == nil {
			err = errors.New("нет подходящего ответа на запрос")
		}
	}
	pr.Error = err.Error()
	return battery.Empty(battery.Mouse, "Нет ответа / устройство спит"), pr, err
}

func Poll() Snapshot {
	snap := Snapshot{At: time.Now(), Samples: [2]battery.Sample{battery.Empty(battery.Mouse, "Подключите мышь / приёмник"), battery.Empty(battery.Headset, "Подключите приёмник V9 Pro")}}
	devices, err := Enumerate()
	snap.Devices = devices
	if err != nil {
		snap.Error = err.Error()
	}
	// Query the cable first. A sleeping receiver must not override a live cable reading.
	sort.SliceStable(devices, func(i, j int) bool {
		_, a := battery.Classify(devices[i])
		_, b := battery.Classify(devices[j])
		return a && !b
	})
	for _, d := range devices {
		p, wired := battery.Classify(d)
		if p == battery.Unsupported {
			continue
		}
		kind := battery.Mouse
		if p == battery.MCHOSE {
			kind = battery.Headset
		}
		snap.Present[kind] = true
		if snap.Samples[kind].Percent >= 0 {
			continue
		}
		if d.Input > 4096 || d.Output > 4096 {
			continue
		}
		path, _ := syscall.UTF16PtrFromString(d.Path)
		h, e := syscall.CreateFile(path, 0xc0000000, 3, nil, 3, 0x40000000, 0)
		if e != nil {
			snap.Probes = append(snap.Probes, Probe{Device: d, Protocol: p, Error: e.Error()})
			snap.Samples[kind] = battery.Empty(kind, "Интерфейс занят / нет доступа")
			continue
		}
		s, pr, e := exchange(h, d, p, wired, 7)
		snap.Probes = append(snap.Probes, pr)
		if e == nil && p == battery.ATKNordic {
			charge, pr2, e2 := exchange(h, d, p, wired, 0x0f)
			snap.Probes = append(snap.Probes, pr2)
			if e2 == nil {
				s.Charging = charge.Charging
				s.ChargeKnown = true
			}
		}
		syscall.CloseHandle(h)
		if e != nil {
			s = battery.Empty(kind, "Нет ответа · спит / выключено")
			s.Wired = wired
		}
		s.Kind = kind
		if s.Wired && s.Percent < 0 {
			s.Detail = "USB · заряд не получен"
		}
		snap.Samples[kind] = s
	}
	// A cable-only headset may expose another USB product ID. Do not query unknown IDs.
	if !snap.Present[battery.Headset] {
		for _, d := range devices {
			name := strings.ToLower(d.Product)
			if d.VID == 0x291d && strings.Contains(name, "v9") && !strings.Contains(name, "dongle") {
				snap.Samples[battery.Headset] = battery.Empty(battery.Headset, "USB · заряд недоступен")
				snap.Samples[battery.Headset].Wired = true
				break
			}
		}
	}
	return snap
}
