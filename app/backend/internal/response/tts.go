package response

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/sbrw-evc/umbrella-monitoring/app/backend/internal/model"
)

// Audio formats a voice delivery asks for.
const (
	FormatWAV = "wav"
	FormatOGG = "ogg"
)

// maxAudio is the largest answer of a speech server taken.
const maxAudio = 20 << 20

// TTS is a client of a self-hosted text-to-speech server.
type TTS struct {
	set    model.TTSSettings
	key    string
	client *http.Client
}

func NewTTS(set model.TTSSettings, apiKey string, client *http.Client) *TTS {
	return &TTS{set: set, key: apiKey, client: client}
}

// Opus reports whether the engine gives OGG/Opus itself (Telegram voice messages need it).
func (t *TTS) Opus() bool {
	return t.set.Provider == model.TTSRHVoice || t.set.Provider == model.TTSOpenAI
}

// Speak turns text into audio in the voice of the locale: WAV or, when the engine can, OGG/Opus.
func (t *TTS) Speak(ctx context.Context, text, locale, format string) ([]byte, error) {
	base := strings.TrimRight(t.set.URL, "/")
	if base == "" {
		return nil, errors.New("the speech server address is not set")
	}
	voice := t.set.Voices[locale]
	var req *http.Request
	var err error
	switch t.set.Provider {
	case model.TTSPiper:
		body := map[string]string{"text": text}
		if voice != "" {
			body["voice"] = voice
		}
		req, err = jsonRequest(ctx, base, body)
	case model.TTSRHVoice:
		q := url.Values{"text": {text}, "format": {map[string]string{FormatWAV: "wav", FormatOGG: "opus"}[format]}}
		if voice != "" {
			q.Set("voice", voice)
		}
		if !strings.HasSuffix(base, "/say") {
			base += "/say"
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	case model.TTSOpenAI:
		if !strings.HasSuffix(base, "/audio/speech") {
			if !strings.HasSuffix(base, "/v1") {
				base += "/v1"
			}
			base += "/audio/speech"
		}
		req, err = jsonRequest(ctx, base, map[string]string{"model": cmp.Or(t.set.Model, "tts-1"), "input": text, "voice": cmp.Or(voice, "alloy"),
			"response_format": map[string]string{FormatWAV: "wav", FormatOGG: "opus"}[format]})
	default:
		return nil, fmt.Errorf("unknown speech engine %q", t.set.Provider)
	}
	if err != nil {
		return nil, fmt.Errorf("speech server: %v", err)
	}
	if t.key != "" {
		req.Header.Set("Authorization", "Bearer "+t.key)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("speech server: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAudio))
	if err != nil {
		return nil, fmt.Errorf("speech server: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, &APIError{Service: "speech server", Status: resp.StatusCode, Msg: errorMessage(data)}
	}
	if len(data) < 64 {
		return nil, errors.New("speech server: the answer has no audio")
	}
	return data, nil
}

func jsonRequest(ctx context.Context, endpoint string, body any) (*http.Request, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// pcm is mono 16-bit audio.
type pcm struct {
	rate    int
	samples []int16
}

// decodeWAV reads PCM (8, 16, 24, 32-bit integer or 32-bit float) WAV of any rate and channels
// and mixes it down to mono. Streaming servers write a data size of 0 or 0xFFFFFFFF: the rest of
// the file is the data then.
func decodeWAV(b []byte) (pcm, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return pcm{}, errors.New("the speech server did not answer with WAV audio")
	}
	var format, channels, bits int
	var rate int
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		body := pos + 8
		switch id {
		case "fmt ":
			if body+16 > len(b) {
				return pcm{}, errors.New("WAV: short format chunk")
			}
			format = int(binary.LittleEndian.Uint16(b[body:]))
			channels = int(binary.LittleEndian.Uint16(b[body+2:]))
			rate = int(binary.LittleEndian.Uint32(b[body+4:]))
			bits = int(binary.LittleEndian.Uint16(b[body+14:]))
			if format == 0xFFFE && size >= 26 && body+26 <= len(b) {
				format = int(binary.LittleEndian.Uint16(b[body+24:]))
			}
		case "data":
			if channels == 0 {
				return pcm{}, errors.New("WAV: data before format")
			}
			end := body + size
			if size == 0 || size == 0xFFFFFFFF || end > len(b) {
				end = len(b)
			}
			return mixDown(b[body:end], format, channels, bits, rate)
		}
		pos = body + size + size%2
	}
	return pcm{}, errors.New("WAV: no audio data")
}

func mixDown(data []byte, format, channels, bits, rate int) (pcm, error) {
	if rate <= 0 || channels <= 0 || channels > 8 {
		return pcm{}, errors.New("WAV: bad format")
	}
	bytesPer := bits / 8
	if (format != 1 && format != 3) || (format == 3 && bits != 32) || bytesPer < 1 || bytesPer > 4 {
		return pcm{}, fmt.Errorf("WAV: format %d with %d bits is not supported", format, bits)
	}
	frame := bytesPer * channels
	n := len(data) / frame
	out := make([]int16, n)
	for i := range n {
		sum := 0.0
		for c := range channels {
			s := data[i*frame+c*bytesPer:]
			var v float64
			switch {
			case format == 3:
				v = float64(math.Float32frombits(binary.LittleEndian.Uint32(s)))
			case bytesPer == 1:
				v = (float64(s[0]) - 128) / 128
			case bytesPer == 2:
				v = float64(int16(binary.LittleEndian.Uint16(s))) / 32768
			case bytesPer == 3:
				v = float64(int32(uint32(s[0])<<8|uint32(s[1])<<16|uint32(s[2])<<24)>>8) / 8388608
			default:
				v = float64(int32(binary.LittleEndian.Uint32(s))) / 2147483648
			}
			sum += v
		}
		v := max(-1, min(1, sum/float64(channels)))
		out[i] = int16(math.Round(v * 32767))
	}
	return pcm{rate: rate, samples: out}, nil
}

// resample changes the rate by linear interpolation; speech survives it well.
func (p pcm) resample(rate int) pcm {
	if p.rate == rate || len(p.samples) == 0 {
		return pcm{rate: rate, samples: p.samples}
	}
	n := int(int64(len(p.samples)) * int64(rate) / int64(p.rate))
	out := make([]int16, n)
	step := float64(p.rate) / float64(rate)
	for i := range n {
		x := float64(i) * step
		j := int(x)
		f := x - float64(j)
		a := float64(p.samples[min(j, len(p.samples)-1)])
		b := float64(p.samples[min(j+1, len(p.samples)-1)])
		out[i] = int16(math.Round(a + (b-a)*f))
	}
	return pcm{rate: rate, samples: out}
}

// wav writes the audio as 16-bit mono PCM WAV.
func (p pcm) wav() []byte {
	size := len(p.samples) * 2
	b := make([]byte, 44+size)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+size))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(p.rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(p.rate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(size))
	for i, s := range p.samples {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(s))
	}
	return b
}

// callRate is what Microsoft Teams plays: 16 kHz mono 16-bit PCM WAV.
const callRate = 16000

// toCallWAV turns any PCM WAV into the format Teams plays.
func toCallWAV(b []byte) ([]byte, error) {
	p, err := decodeWAV(b)
	if err != nil {
		return nil, err
	}
	return p.resample(callRate).wav(), nil
}

// lookFFmpeg finds ffmpeg; tests replace it.
var lookFFmpeg = func() (string, error) { return exec.LookPath("ffmpeg") }

// toOpus turns WAV into OGG/Opus with ffmpeg; errNoFFmpeg when it is not installed.
func toOpus(ctx context.Context, wav []byte) ([]byte, error) {
	bin, err := lookFFmpeg()
	if err != nil {
		return nil, errNoFFmpeg
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-loglevel", "error", "-f", "wav", "-i", "pipe:0", "-c:a", "libopus", "-b:a", "32k", "-f", "ogg", "pipe:1")
	cmd.Stdin = bytes.NewReader(wav)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

var errNoFFmpeg = errors.New("ffmpeg is not installed")
