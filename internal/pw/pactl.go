package pw

// pactl 実装。`pactl --format=json list (sinks|sink-inputs)` の JSON を
// パースしつつ、操作系は `pactl set-*` を fork するシンプルなアダプタ。
// 値の単位:
//   pulse の volume value 65536 = 100% = 0 dB。VolumePct はチャンネル平均
//   から算出。set 側は "{pct}%" 文字列で投げる。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// PactlBackend は pactl shell out 実装。
type PactlBackend struct{}

// NewPactlBackend で組み立てる。pactl が PATH に居る前提。
func NewPactlBackend() *PactlBackend { return &PactlBackend{} }

// pactl の sinks 出力を表すレコード。
type pactlSink struct {
	Index       int                          `json:"index"`
	Name        string                       `json:"name"`
	Description string                       `json:"description"`
	State       string                       `json:"state"`
	Mute        bool                         `json:"mute"`
	Volume      map[string]pactlVolumeChan   `json:"volume"`
	Properties  map[string]string            `json:"properties"`
}

type pactlSinkInput struct {
	Index      int                        `json:"index"`
	Sink       int                        `json:"sink"`
	Mute       bool                       `json:"mute"`
	Corked     bool                       `json:"corked"`
	Volume     map[string]pactlVolumeChan `json:"volume"`
	Properties map[string]string          `json:"properties"`
}

type pactlVolumeChan struct {
	Value int `json:"value"`
}

// avgVolumePct はチャネル全体の平均 volume を百分率で返す。
func avgVolumePct(vol map[string]pactlVolumeChan) int {
	if len(vol) == 0 {
		return 0
	}
	sum := 0
	for _, c := range vol {
		sum += c.Value
	}
	avg := sum / len(vol)
	// 65536 = 100%
	return int(float64(avg) * 100.0 / 65536.0)
}

// runPactlJSON は pactl コマンドを stdout JSON で読み出す。
// pactl は環境による warning を stderr に出す事があるので分離する。
func runPactlJSON(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "pactl", append([]string{"--format=json"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pactl %v: %w (stderr: %s)", args, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func runPactl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "pactl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pactl %v: %w (stderr: %s)", args, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (b *PactlBackend) Sinks(ctx context.Context) ([]Sink, error) {
	out, err := runPactlJSON(ctx, "list", "sinks")
	if err != nil {
		return nil, err
	}
	var raw []pactlSink
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse sinks: %w", err)
	}

	defOut, err := exec.CommandContext(ctx, "pactl", "get-default-sink").Output()
	if err != nil {
		return nil, fmt.Errorf("get-default-sink: %w", err)
	}
	defaultName := strings.TrimSpace(string(defOut))

	res := make([]Sink, 0, len(raw))
	for _, s := range raw {
		desc := s.Description
		if desc == "" || desc == "(null)" {
			if d, ok := s.Properties["device.description"]; ok && d != "" {
				desc = d
			} else if n, ok := s.Properties["node.nick"]; ok && n != "" {
				desc = n
			} else {
				desc = s.Name
			}
		}
		res = append(res, Sink{
			ID:          uint32(s.Index),
			Name:        s.Name,
			Description: desc,
			Default:     s.Name == defaultName,
			VolumePct:   avgVolumePct(s.Volume),
			Mute:        s.Mute,
			State:       s.State,
		})
	}
	return res, nil
}

func (b *PactlBackend) Streams(ctx context.Context) ([]Stream, error) {
	out, err := runPactlJSON(ctx, "list", "sink-inputs")
	if err != nil {
		return nil, err
	}
	var raw []pactlSinkInput
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse sink-inputs: %w", err)
	}

	res := make([]Stream, 0, len(raw))
	for _, s := range raw {
		name := firstProp(s.Properties, "application.name", "node.name", "media.name")
		res = append(res, Stream{
			ID:        uint32(s.Index),
			Name:      name,
			AppBinary: s.Properties["application.process.binary"],
			MediaName: s.Properties["media.name"],
			SinkID:    uint32(s.Sink),
			VolumePct: avgVolumePct(s.Volume),
			Mute:      s.Mute,
			Corked:    s.Corked,
		})
	}
	return res, nil
}

func firstProp(p map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := p[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func (b *PactlBackend) SetStreamVolume(ctx context.Context, id uint32, pct int) error {
	if pct < 0 {
		return errors.New("volume_pct must be >= 0")
	}
	return runPactl(ctx, "set-sink-input-volume", strconv.FormatUint(uint64(id), 10), strconv.Itoa(pct)+"%")
}

func (b *PactlBackend) SetStreamMute(ctx context.Context, id uint32, mute bool) error {
	v := "0"
	if mute {
		v = "1"
	}
	return runPactl(ctx, "set-sink-input-mute", strconv.FormatUint(uint64(id), 10), v)
}

func (b *PactlBackend) SetSinkVolume(ctx context.Context, id uint32, pct int) error {
	if pct < 0 {
		return errors.New("volume_pct must be >= 0")
	}
	return runPactl(ctx, "set-sink-volume", strconv.FormatUint(uint64(id), 10), strconv.Itoa(pct)+"%")
}

func (b *PactlBackend) SetSinkMute(ctx context.Context, id uint32, mute bool) error {
	v := "0"
	if mute {
		v = "1"
	}
	return runPactl(ctx, "set-sink-mute", strconv.FormatUint(uint64(id), 10), v)
}

func (b *PactlBackend) SetDefaultSink(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	return runPactl(ctx, "set-default-sink", name)
}
