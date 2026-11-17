package battery

import (
	"bytes"
	"testing"
	"time"
)

func TestCapturedNordicBattery(t *testing.T) {
	// Hardware capture from cobanov/atkctl, ATK A9 Plus NK, same Nordic 54 wire format.
	b := []byte{8, 0x72, 0, 0x3b, 0, 7, 1, 100, 0x30, 0x11}
	s, ok := Parse(ATKNordic, b, true, 7)
	if !ok || s.Percent != 100 || !s.Wired {
		t.Fatalf("capture: %+v %t", s, ok)
	}
	// Charge state is a separate query; byte 3 of a battery reply is not decoded as charge.
	if s.ChargeKnown || s.Charging {
		t.Fatal("inferred a charging state from the reply header")
	}
	s, ok = Parse(ATKNordic, []byte{8, 0x72, 0, 0, 0, 0x0f, 1, 1}, false, 0x0f)
	if !ok || !s.ChargeKnown || !s.Charging {
		t.Fatal("charging reply not read")
	}
}
func TestNordicRejectsOtherCommandsAndErrors(t *testing.T) {
	good := []byte{8, 0x72, 0, 5, 0, 7, 1, 83}
	for _, index := range []int{0, 1, 2, 5, 6, 7} {
		b := append([]byte(nil), good...)
		b[index] = 0xff
		if _, ok := Parse(ATKNordic, b, false, 7); ok {
			t.Fatalf("accepted corruption at %d", index)
		}
	}
	if _, ok := Parse(ATKNordic, []byte{8, 0x72, 0, 0, 0, 0x0f, 1, 1}, false, 7); ok {
		t.Fatal("wrong command")
	}
}
func TestRequestFixtures(t *testing.T) {
	cases := []struct {
		p      Protocol
		wired  bool
		cmd    byte
		prefix []byte
		length int
	}{
		{ATKClassic, false, 7, []byte{8, 4, 0, 0, 0, 0, 0}, 17},
		{ATKNordic, false, 7, []byte{8, 0x7d, 0x72, 2, 0, 1, 7, 1}, 64},
		{ATKNordic, true, 7, []byte{8, 0x7c, 0x72, 2, 0, 0, 7, 1}, 64},
		{ATKNordic, false, 0x0f, []byte{8, 0x85, 0x72, 2, 0, 1, 0x0f, 1}, 64},
		{MCHOSE, false, 0, []byte{0x55, 0x65, 1}, 64},
	}
	for _, c := range cases {
		b := Request(c.p, c.wired, c.cmd)
		if len(b) != c.length || !bytes.Equal(b[:len(c.prefix)], c.prefix) {
			t.Fatalf("bad command: % X", b)
		}
	}
	if Request(ATKClassic, false, 7)[16] != 0x49 {
		t.Fatal("classic checksum")
	}
}
func TestMCHOSEVerifiedReplyAndSleep(t *testing.T) {
	for _, level := range []byte{0, 1, 80, 100} {
		s, ok := Parse(MCHOSE, []byte{0x55, 0x65, level, 2}, false, 7)
		if !ok || s.Kind != Headset || s.Percent != int(level) || s.Charging {
			t.Fatal(s, ok)
		}
	}
	s, ok := Parse(MCHOSE, []byte{0x55, 0x65, 47, 3}, false, 7)
	if !ok || !s.Charging {
		t.Fatal(s, ok)
	}
	s, ok = Parse(MCHOSE, []byte{0x55, 0x65, 80, 26}, false, 7)
	if !ok || s.Percent != -1 {
		t.Fatal("sleeping headset gave a current level")
	}
	for _, b := range [][]byte{{0x55, 0x65}, {0x55, 0x65, 101, 2}, {0x55, 0x11, 80, 2}} {
		if _, ok := Parse(MCHOSE, b, false, 7); ok {
			t.Fatal("bad headset frame accepted")
		}
	}
}
func TestEchoIsNeverBattery(t *testing.T) {
	for _, p := range []Protocol{ATKClassic, ATKNordic, MCHOSE} {
		req := Request(p, false, 7)
		padded := append(append([]byte(nil), req...), make([]byte, 16)...)
		for _, b := range [][]byte{req, padded} {
			if _, ok := Parse(p, b, false, 7); ok {
				t.Fatalf("echo accepted for %v", p)
			}
		}
	}
	if _, ok := Parse(MCHOSE, []byte{0x55, 0x65, 1, 0}, false, 7); ok {
		t.Fatal("short echo accepted")
	}
}
func TestClassicChecksum(t *testing.T) {
	b := []byte{8, 4, 0, 0, 0, 4, 93, 0, 15, 160, 0, 0, 0, 0, 0, 0, 0x39}
	s, ok := Parse(ATKClassic, b, true, 7)
	if !ok || s.Percent != 93 {
		t.Fatal(s, ok)
	}
	b[6] = 80
	if _, ok := Parse(ATKClassic, b, true, 7); ok {
		t.Fatal("bad checksum accepted")
	}
}
func TestClassifyInterfaces(t *testing.T) {
	for _, pid := range []uint16{0x12ce, 0x1312} {
		p, w := Classify(Device{VID: 0x373b, PID: pid, Page: 0xff05, Usage: 1, Input: 64, Output: 64})
		if p != ATKNordic || !w {
			t.Fatal("Phantom cable not classified")
		}
	}
	d := Device{VID: 0x373b, PID: 0x12cd, Page: 0xff05, Usage: 1, Input: 64, Output: 64}
	p, w := Classify(d)
	if p != ATKNordic || w {
		t.Fatal("Nano receiver not classified")
	}
	// Sibling keyboard/mouse collections must never be queried.
	for _, page := range []uint16{1, 7, 0xff01, 0xff02} {
		d.Page = page
		if p, _ := Classify(d); p != Unsupported {
			t.Fatalf("wrong collection %x", page)
		}
	}
	h := Device{VID: 0x291d, PID: 0x385d, Page: 0xff90, Usage: 1, Input: 64, Output: 64}
	if p, _ := Classify(h); p != MCHOSE {
		t.Fatal("V9 not classified")
	}
	h.PID++
	if p, _ := Classify(h); p != Unsupported {
		t.Fatal("unknown headset queried")
	}
}
func TestStaleMouseExpiresAndHeadsetClears(t *testing.T) {
	now := time.Now()
	old := Sample{Kind: Mouse, Percent: 80, At: now.Add(-time.Minute)}
	failed := Empty(Mouse, "sleep")
	s := Merge(old, failed, now, true)
	if s.Percent != 80 || !s.Stale || s.Value() != "~80%" {
		t.Fatal(s)
	}
	if Merge(old, failed, now, false).Percent != -1 {
		t.Fatal("unplugged receiver retains level")
	}
	if Merge(old, failed, now.Add(11*time.Minute), true).Percent != -1 {
		t.Fatal("stale level never expires")
	}
	failed.Wired = true
	if Merge(old, failed, now, true).Percent != -1 {
		t.Fatal("cable failure shown as sleeping wireless")
	}
	old.Kind = Headset
	if Merge(old, Empty(Headset, "off"), now, true).Percent != -1 {
		t.Fatal("headset retained")
	}
}
func TestAlertHysteresis(t *testing.T) {
	a := Alert{}
	s := Sample{Percent: 20}
	if !a.Update(s, 20) || a.Update(s, 20) {
		t.Fatal("duplicate / missing alert")
	}
	s.Percent = 24
	a.Update(s, 20)
	s.Percent = 19
	if a.Update(s, 20) {
		t.Fatal("jitter rearms notification")
	}
	s.Percent = 30
	a.Update(s, 20)
	s.Percent = 10
	if !a.Update(s, 20) {
		t.Fatal("not rearmed after charge")
	}
	for _, q := range []Sample{{Percent: -1}, {Percent: 10, Stale: true}, {Percent: 10, Charging: true}, {Percent: 10, Wired: true}} {
		b := Alert{}
		if b.Update(q, 20) {
			t.Fatal("false low battery notification", q)
		}
	}
}
func FuzzParse(f *testing.F) {
	f.Add([]byte{0x55, 0x65, 80, 2})
	f.Add([]byte{8, 0x72, 0, 5, 0, 7, 1, 83})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, p := range []Protocol{ATKClassic, ATKNordic, MCHOSE} {
			s, ok := Parse(p, b, false, 7)
			if ok && (s.Percent < -1 || s.Percent > 100) {
				t.Fatal("impossible percentage", s)
			}
		}
	})
}
