// Package battery implements only telemetry commands. It never changes device settings.
package battery

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

type Kind int

const (
	Mouse Kind = iota
	Headset
)

type Protocol int

const (
	Unsupported Protocol = iota
	ATKClassic
	ATKNordic
	MCHOSE
)

type Device struct {
	Path                                          string `json:"-"`
	Product                                       string
	VID, PID, Page, Usage, Input, Output, Feature uint16
}
type Sample struct {
	Kind        Kind
	Percent     int // -1 means unavailable; zero is a real reading.
	Charging    bool
	ChargeKnown bool
	Wired       bool
	Stale       bool
	Connection  string
	Detail      string
	At          time.Time
}

func Name(k Kind) string {
	if k == Mouse {
		return "ATK Phantom"
	}
	return "MCHOSE V9 Pro"
}
func Empty(k Kind, detail string) Sample { return Sample{Kind: k, Percent: -1, Detail: detail} }
func (s Sample) Value() string {
	if s.Percent < 0 {
		return "—"
	}
	prefix := ""
	if s.Stale {
		prefix = "~"
	}
	return fmt.Sprintf("%s%d%%", prefix, s.Percent)
}
func (s Sample) Status() string {
	if s.Stale {
		return "Спит · последнее значение"
	}
	if s.Percent < 0 {
		return s.Detail
	}
	if s.Charging {
		return "Заряжается · " + s.Connection
	}
	if s.Wired {
		return "USB · питание по кабелю"
	}
	return s.Connection
}

// Exact Phantom cable IDs and standard ATK receivers from the ATK Hub catalog.
var receiverIDs = map[uint16]bool{0x1155: true, 0x11dc: true, 0x1216: true, 0x124f: true, 0x1290: true, 0x1291: true, 0x12af: true, 0x12cd: true, 0x12d1: true, 0x12ea: true, 0x12f5: true, 0x1302: true, 0x1315: true, 0x1332: true, 0x133e: true, 0x133f: true, 0x1346: true, 0x1347: true, 0x1348: true, 0x134b: true}

func Classify(d Device) (Protocol, bool) {
	if d.VID == 0x291d && d.PID == 0x385d && d.Page == 0xff90 && d.Usage == 1 && d.Input == 64 && d.Output == 64 {
		return MCHOSE, false
	}
	if d.VID != 0x373b {
		return Unsupported, false
	}
	wired := d.PID == 0x1296 || d.PID == 0x12ce || d.PID == 0x1312
	product := strings.ToLower(d.Product)
	receiver := receiverIDs[d.PID] || (strings.Contains(product, "mouse") && strings.Contains(product, "dongle"))
	if !wired && !receiver {
		return Unsupported, false
	}
	if d.Page == 0xff05 && d.Usage == 1 && d.Input >= 64 && d.Output == 64 {
		return ATKNordic, wired
	}
	if (wired || receiver) && (d.Page == 0xff02 || d.Page == 0xff04) && d.Usage == 2 && d.Input >= 17 && d.Output >= 17 {
		return ATKClassic, wired
	}
	return Unsupported, false
}
func Request(p Protocol, wired bool, command byte) []byte {
	if p == MCHOSE {
		b := make([]byte, 64)
		copy(b, []byte{0x55, 0x65, 1})
		return b
	}
	if p == ATKClassic {
		b := make([]byte, 17)
		b[0] = 8
		b[1] = 4
		b[16] = 0x49
		return b
	}
	if p != ATKNordic {
		return nil
	}
	b := make([]byte, 64)
	b[0] = 8
	b[2] = 0x72
	b[3] = 2
	b[6] = command
	b[7] = 1
	if !wired {
		b[5] = 1
	}
	for _, v := range b[2:] {
		b[1] += v
	}
	return b
}

// Parse rejects mismatched commands, acknowledgements, corrupt percentages and echoes.
// Nordic reply offsets differ from request offsets; requests include a checksum byte.
func Parse(p Protocol, b []byte, wired bool, command byte) (Sample, bool) {
	s := Sample{Kind: Mouse, Percent: -1, Wired: wired, Connection: "2,4 ГГц", At: time.Now()}
	if wired {
		s.Connection = "USB"
	}
	switch p {
	case MCHOSE:
		s.Kind = Headset
		if len(b) < 4 || b[0] != 0x55 || b[1] != 0x65 || b[2] > 100 || echo(b, Request(MCHOSE, false, 0)) {
			return s, false
		}
		// 0x1a is sleep; it must not masquerade as a live battery level.
		if b[3] == 0x1a {
			s.Detail = "Наушники спят / нет связи"
			return s, true
		}
		s.Percent = int(b[2])
		s.ChargeKnown = b[3] == 2 || b[3] == 3 || b[3] == 4
		s.Charging = b[3] == 3
		s.Detail = fmt.Sprintf("Код состояния: 0x%02X", b[3])
		return s, true
	case ATKClassic:
		if len(b) < 17 || b[0] != 8 || b[1] != 4 || b[2] != 0 || b[6] > 100 || b[7] > 1 || echo(b, Request(p, wired, 4)) {
			return s, false
		}
		var sum byte
		for _, v := range b[:17] {
			sum += v
		}
		if sum != 0x55 {
			return s, false
		}
		s.Percent = int(b[6])
		s.Charging = b[7] == 1
		s.Wired = b[7] == 1
		s.Connection = "2,4 ГГц"
		if s.Wired {
			s.Connection = "USB"
		}
		s.ChargeKnown = true
		return s, true
	case ATKNordic:
		if len(b) < 8 || b[0] != 8 || b[1] != 0x72 || b[2] != 0 || b[5] != command || b[6] != 1 {
			return s, false
		}
		if command == 7 {
			if b[7] > 100 {
				return s, false
			}
			s.Percent = int(b[7])
		} else if command == 0x0f {
			if b[7] > 1 {
				return s, false
			}
			s.Charging = b[7] == 1
			s.ChargeKnown = true
		} else {
			return s, false
		}
		return s, true
	}
	return s, false
}

func echo(a, b []byte) bool {
	return bytes.Equal(bytes.TrimRight(a, "\x00"), bytes.TrimRight(b, "\x00"))
}

// Retain a sleeping mouse briefly, always visibly stale. Never retain a headset value.
func Merge(previous, current Sample, now time.Time, present bool) Sample {
	if current.Percent >= 0 {
		return current
	}
	if current.Kind == Mouse && !current.Wired && present && previous.Percent >= 0 && now.Sub(previous.At) >= 0 && now.Sub(previous.At) <= 10*time.Minute {
		previous.Stale = true
		previous.Charging = false
		previous.Detail = current.Detail
		return previous
	}
	return current
}

type Alert struct{ Warned bool }

func (a *Alert) Update(s Sample, threshold int) bool {
	if s.Percent < 0 || s.Stale {
		return false
	}
	if s.Charging || s.Wired || s.Percent > threshold+5 {
		a.Warned = false
		return false
	}
	if s.Percent <= threshold && !a.Warned {
		a.Warned = true
		return true
	}
	return false
}
