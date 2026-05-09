// Package pw 内の API 共通型と Backend interface。
//
// 現状の Backend は pactl shell out で実装している (pactl.go)。将来的には
// libpipewire の registry events を cgo で食わせて event-driven に置き換える
// 想定だが、それまでの繋ぎ。Backend interface 越しに使うので server 側を
// 触らずに差し替えできる。
package pw

import "context"

// Stream は sink-input (アプリ毎の出力ストリーム)。
type Stream struct {
	ID        uint32 `json:"id"`         // pactl index
	Name      string `json:"name"`       // application.name または node.name fallback
	AppBinary string `json:"app_binary"` // application.process.binary
	MediaName string `json:"media_name"` // media.name
	SinkID    uint32 `json:"sink_id"`    // attached sink
	VolumePct int    `json:"volume_pct"` // 0..N (100% = 65536)
	Mute      bool   `json:"mute"`
	Corked    bool   `json:"corked"`
}

// Sink は出力デバイス。
type Sink struct {
	ID          uint32 `json:"id"`          // pactl index
	Name        string `json:"name"`        // node.name (例 alsa_output.xxx)
	Description string `json:"description"` // device.description or node.nick
	Default     bool   `json:"default"`
	VolumePct   int    `json:"volume_pct"`
	Mute        bool   `json:"mute"`
	State       string `json:"state"` // RUNNING / IDLE / SUSPENDED
}

// Backend は mixer 状態の取得 + 操作インターフェース。
type Backend interface {
	Streams(ctx context.Context) ([]Stream, error)
	Sinks(ctx context.Context) ([]Sink, error)

	SetStreamVolume(ctx context.Context, id uint32, pct int) error
	SetStreamMute(ctx context.Context, id uint32, mute bool) error

	SetSinkVolume(ctx context.Context, id uint32, pct int) error
	SetSinkMute(ctx context.Context, id uint32, mute bool) error

	SetDefaultSink(ctx context.Context, name string) error
}
