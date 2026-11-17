package main

import (
	"syscall"
	"unsafe"
)

var user = syscall.NewLazyDLL("user32.dll")
var gdi = syscall.NewLazyDLL("gdi32.dll")
var shell = syscall.NewLazyDLL("shell32.dll")
var kernel = syscall.NewLazyDLL("kernel32.dll")
var advapi = syscall.NewLazyDLL("advapi32.dll")
var dwm = syscall.NewLazyDLL("dwmapi.dll")
var registerClass = user.NewProc("RegisterClassExW")
var createWindow = user.NewProc("CreateWindowExW")
var defWindowProc = user.NewProc("DefWindowProcW")
var getMessage = user.NewProc("GetMessageW")
var translateMessage = user.NewProc("TranslateMessage")
var dispatchMessage = user.NewProc("DispatchMessageW")
var postMessage = user.NewProc("PostMessageW")
var postQuit = user.NewProc("PostQuitMessage")
var showWindow = user.NewProc("ShowWindow")
var setWindowPos = user.NewProc("SetWindowPos")
var foreground = user.NewProc("SetForegroundWindow")
var getForeground = user.NewProc("GetForegroundWindow")
var getWindowRect = user.NewProc("GetWindowRect")
var findWindow = user.NewProc("FindWindowW")
var invalidate = user.NewProc("InvalidateRect")
var beginPaint = user.NewProc("BeginPaint")
var endPaint = user.NewProc("EndPaint")
var fillRect = user.NewProc("FillRect")
var drawText = user.NewProc("DrawTextW")
var getCursorPos = user.NewProc("GetCursorPos")
var setTimer = user.NewProc("SetTimer")
var createPopup = user.NewProc("CreatePopupMenu")
var appendMenu = user.NewProc("AppendMenuW")
var trackMenu = user.NewProc("TrackPopupMenu")
var destroyMenu = user.NewProc("DestroyMenu")
var messageBox = user.NewProc("MessageBoxW")
var getDC = user.NewProc("GetDC")
var releaseDC = user.NewProc("ReleaseDC")
var createIcon = user.NewProc("CreateIconIndirect")
var destroyIcon = user.NewProc("DestroyIcon")
var loadCursor = user.NewProc("LoadCursorW")
var registerMessage = user.NewProc("RegisterWindowMessageW")
var monitorFromWindow = user.NewProc("MonitorFromWindow")
var getMonitorInfo = user.NewProc("GetMonitorInfoW")
var setDPI = user.NewProc("SetProcessDpiAwarenessContext")
var getDPI = user.NewProc("GetDpiForWindow")
var releaseCapture = user.NewProc("ReleaseCapture")
var sendMessage = user.NewProc("SendMessageW")
var notify = shell.NewProc("Shell_NotifyIconW")
var shellExecute = shell.NewProc("ShellExecuteW")
var createBrush = gdi.NewProc("CreateSolidBrush")
var createPen = gdi.NewProc("CreatePen")
var createFont = gdi.NewProc("CreateFontW")
var selectObject = gdi.NewProc("SelectObject")
var deleteObject = gdi.NewProc("DeleteObject")
var getStock = gdi.NewProc("GetStockObject")
var setBkMode = gdi.NewProc("SetBkMode")
var setTextColor = gdi.NewProc("SetTextColor")
var roundRect = gdi.NewProc("RoundRect")
var ellipse = gdi.NewProc("Ellipse")
var moveTo = gdi.NewProc("MoveToEx")
var lineTo = gdi.NewProc("LineTo")
var arc = gdi.NewProc("Arc")
var createDIB = gdi.NewProc("CreateDIBSection")
var createBitmap = gdi.NewProc("CreateBitmap")
var createDC = gdi.NewProc("CreateCompatibleDC")
var deleteDC = gdi.NewProc("DeleteDC")
var setAttribute = dwm.NewProc("DwmSetWindowAttribute")

type point struct{ X, Y int32 }
type rect struct{ L, T, R, B int32 }
type message struct {
	Window         uintptr
	ID             uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}
type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type paintStruct struct {
	DC                 uintptr
	Erase              int32
	Paint              rect
	Restore, IncUpdate int32
	Reserved           [32]byte
}
type monitorInfo struct {
	Size          uint32
	Monitor, Work rect
	Flags         uint32
}
type iconInfo struct {
	IsIcon      int32
	X, Y        uint32
	Mask, Color uintptr
}
type bitmapHeader struct {
	Size                   uint32
	Width, Height          int32
	Planes, BitCount       uint16
	Compression, SizeImage uint32
	XPels, YPels           int32
	Colors, Important      uint32
}
type bitmapInfo struct {
	Header bitmapHeader
	Colors [1]uint32
}
type notifyData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                syscall.GUID
	BalloonIcon         uintptr
}

var _ [976 - unsafe.Sizeof(notifyData{})]byte
var _ [unsafe.Sizeof(notifyData{}) - 976]byte
var _ [80 - unsafe.Sizeof(windowClass{})]byte
var _ [unsafe.Sizeof(windowClass{}) - 80]byte

func ptr(s string) *uint16     { return syscall.StringToUTF16Ptr(s) }
func u(v int) uintptr          { return uintptr(int64(v)) }
func rgb(r, g, b byte) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }
func color(hex uint32) uintptr { return rgb(byte(hex>>16), byte(hex>>8), byte(hex)) }
func copyText(dst []uint16, s string) {
	clear(dst)
	v, _ := syscall.UTF16FromString(s)
	copy(dst, v[:min(len(v), len(dst)-1)])
}
func callText(dc uintptr, s string, r rect, size int, weight int, c uintptr, flags uintptr) {
	font, _, _ := createFont.Call(u(-size), 0, 0, 0, u(weight), 0, 0, 0, 1, 0, 0, 4, 0, uintptr(unsafe.Pointer(ptr("Segoe UI"))))
	old, _, _ := selectObject.Call(dc, font)
	setBkMode.Call(dc, 1)
	setTextColor.Call(dc, c)
	drawText.Call(dc, uintptr(unsafe.Pointer(ptr(s))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), flags|0x20|0x8000)
	selectObject.Call(dc, old)
	deleteObject.Call(font)
}
func box(dc uintptr, r rect, radius int, c uintptr) {
	brush, _, _ := createBrush.Call(c)
	oldB, _, _ := selectObject.Call(dc, brush)
	pen, _, _ := createPen.Call(0, 1, c)
	oldP, _, _ := selectObject.Call(dc, pen)
	roundRect.Call(dc, u(int(r.L)), u(int(r.T)), u(int(r.R)), u(int(r.B)), u(radius), u(radius))
	selectObject.Call(dc, oldB)
	selectObject.Call(dc, oldP)
	deleteObject.Call(brush)
	deleteObject.Call(pen)
}

func pictogram(dc uintptr, kind int, x, y, size int, c uintptr) {
	pen, _, _ := createPen.Call(0, u(max(1, size/12)), c)
	old, _, _ := selectObject.Call(dc, pen)
	hollow, _, _ := getStock.Call(5)
	oldB, _, _ := selectObject.Call(dc, hollow)
	line := func(x1, y1, x2, y2 int) { moveTo.Call(dc, u(x1), u(y1), 0); lineTo.Call(dc, u(x2), u(y2)) }
	if kind == 0 {
		roundRect.Call(dc, u(x+size/5), u(y), u(x+size*4/5), u(y+size), u(size/2), u(size/2))
		line(x+size/2, y+size/6, x+size/2, y+size/3)
	} else {
		arc.Call(dc, u(x+size/8), u(y), u(x+size*7/8), u(y+size*7/8), u(x+size*7/8), u(y+size/2), u(x+size/8), u(y+size/2))
		roundRect.Call(dc, u(x+size/12), u(y+size/2), u(x+size/3), u(y+size), u(size/8), u(size/8))
		roundRect.Call(dc, u(x+size*2/3), u(y+size/2), u(x+size*11/12), u(y+size), u(size/8), u(size/8))
	}
	selectObject.Call(dc, old)
	selectObject.Call(dc, oldB)
	deleteObject.Call(pen)
}

func openFile(path string) {
	shellExecute.Call(0, uintptr(unsafe.Pointer(ptr("open"))), uintptr(unsafe.Pointer(ptr(path))), 0, 0, 1)
}
func info(title, body string) {
	messageBox.Call(0, uintptr(unsafe.Pointer(ptr(body))), uintptr(unsafe.Pointer(ptr(title))), 0x40)
}
