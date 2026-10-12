package response

import (
	"encoding/binary"
	"math"
	"testing"
)

// stereoFloatWAV is 22050 Hz stereo 32-bit float audio of n frames.
func stereoFloatWAV(n int) []byte {
	data := make([]byte, n*8)
	for i := range n {
		v := float32(math.Sin(float64(i) / 10))
		binary.LittleEndian.PutUint32(data[i*8:], math.Float32bits(v))
		binary.LittleEndian.PutUint32(data[i*8+4:], math.Float32bits(v))
	}
	b := make([]byte, 0, 80+len(data))
	b = append(b, "RIFF\x00\x00\x00\x00WAVE"...)
	// A chunk before the format, as some servers write.
	b = append(b, "LIST\x04\x00\x00\x00INFO"...)
	fmtc := make([]byte, 24)
	copy(fmtc, "fmt ")
	binary.LittleEndian.PutUint32(fmtc[4:], 16)
	binary.LittleEndian.PutUint16(fmtc[8:], 3)
	binary.LittleEndian.PutUint16(fmtc[10:], 2)
	binary.LittleEndian.PutUint32(fmtc[12:], 22050)
	binary.LittleEndian.PutUint32(fmtc[16:], 22050*8)
	binary.LittleEndian.PutUint16(fmtc[20:], 8)
	binary.LittleEndian.PutUint16(fmtc[22:], 32)
	b = append(b, fmtc...)
	// A streaming server does not know the size: 0xFFFFFFFF.
	b = append(b, "data\xff\xff\xff\xff"...)
	return append(b, data...)
}

func TestToCallWAV(t *testing.T) {
	out, err := toCallWAV(stereoFloatWAV(22050))
	if err != nil {
		t.Fatal(err)
	}
	p, err := decodeWAV(out)
	if err != nil {
		t.Fatal(err)
	}
	if p.rate != 16000 || len(p.samples) != 16000 {
		t.Fatalf("rate %d samples %d", p.rate, len(p.samples))
	}
	if binary.LittleEndian.Uint16(out[22:]) != 1 || binary.LittleEndian.Uint16(out[34:]) != 16 {
		t.Fatal("not 16-bit mono")
	}
	peak := int16(0)
	for _, s := range p.samples {
		peak = max(peak, s)
	}
	if peak < 30000 {
		t.Fatalf("the signal is lost: peak %d", peak)
	}
	if _, err := toCallWAV([]byte("ID3 this is mp3")); err == nil {
		t.Fatal("non-WAV audio is refused")
	}
}

func TestRenderVoice(t *testing.T) {
	got := RenderVoice("Инцидент {number}.  {title}. {unknown} {ack_hint}", map[string]string{"number": "1 0 4 2", "title": "Ошибки", "ack_hint": ""})
	if got != "Инцидент 1 0 4 2. Ошибки. {unknown}" {
		t.Fatalf("got %q", got)
	}
}
