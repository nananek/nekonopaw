// Package pw は libpipewire client の cgo wrapper + 状態キャッシュ。
//
// 動作概要:
//   pw_thread_loop が daemon に常時接続して registry の global event を受け
//   続けている。Audio/Sink および Stream/Output/Audio の Node を bind し、
//   info + Props param 購読で「どの Node が」「volume 何 / mute か」を Go
//   側 cache (nodeState map) に積む。HTTP 側はその cache を mutex 越しに
//   読むだけなので即答できる (pactl shellout ではなくなった)。
//
// SetVolume / SetMute / SetDefaultSink は cgo で SPA POD を組んで pw_node_
// set_param / pw_metadata_set_property を呼ぶ。pactl 依存ゼロ。
//
// volume scale: PulseAudio 系 UI と感覚を揃えるため、UI percent と内部 linear
// amplitude の変換は cube root にする。slider 50% は linear 0.5^3 = 0.125
// (= -18 dB ぐらい) という mapping。
package pw

/*
#cgo pkg-config: libpipewire-0.3
#include <stdlib.h>
#include <stdint.h>
#include "pw_glue.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"unsafe"
)

// Client は in-memory snapshot を保持しつつ Backend を実装する。
type Client struct {
	mu          sync.RWMutex
	nodes       map[uint32]*nodeState
	defaultSink string // node.name (metadata "default.audio.sink" 由来)
}

type nodeState struct {
	id           uint32
	mediaClass   string // "Audio/Sink" or "Stream/Output/Audio"
	nodeName     string
	description  string
	appName      string
	appBinary    string
	mediaName    string
	state        string // node.state (sink 用、RUNNING/IDLE/SUSPENDED)
	volumeLinear float32
	mute         bool
	known        bool // info event を 1 回でも受けたか
}

var (
	clientMu     sync.Mutex
	globalClient *Client
)

// Init は libpipewire の global state 初期化。プロセス起動時 1 回。
func Init()   { C.nekonopaw_init() }
func Deinit() { C.nekonopaw_deinit() }

// NewClient は daemon に接続して event 受信を始める。
func NewClient() (*Client, error) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if globalClient != nil {
		return nil, errors.New("nekonopaw client already initialized")
	}
	c := &Client{nodes: make(map[uint32]*nodeState)}
	globalClient = c
	if rc := int(C.nekonopaw_connect()); rc != 0 {
		globalClient = nil
		return nil, fmt.Errorf("nekonopaw_connect: %d", rc)
	}
	return c, nil
}

// Close で接続を畳む。
func (c *Client) Close() {
	C.nekonopaw_disconnect()
	clientMu.Lock()
	globalClient = nil
	clientMu.Unlock()
}

// Backend interface (types.go)

func (c *Client) Sinks(ctx context.Context) ([]Sink, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var res []Sink
	for _, n := range c.nodes {
		if n.mediaClass != "Audio/Sink" || !n.known {
			continue
		}
		res = append(res, Sink{
			ID:          n.id,
			Name:        n.nodeName,
			Description: firstNonEmpty(n.description, n.nodeName),
			Default:     n.nodeName != "" && n.nodeName == c.defaultSink,
			VolumePct:   linearToPct(n.volumeLinear),
			Mute:        n.mute,
			State:       n.state,
		})
	}
	// Go map の iteration 順は毎回ランダム。ID 昇順に固定して frontend で
	// list の順番が refresh のたびに入れ替わるのを防ぐ。
	sort.Slice(res, func(i, j int) bool { return res[i].ID < res[j].ID })
	return res, nil
}

func (c *Client) Streams(ctx context.Context) ([]Stream, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var res []Stream
	for _, n := range c.nodes {
		if n.mediaClass != "Stream/Output/Audio" || !n.known {
			continue
		}
		name := firstNonEmpty(n.appName, n.nodeName, n.mediaName)
		res = append(res, Stream{
			ID:        n.id,
			Name:      name,
			AppBinary: n.appBinary,
			MediaName: n.mediaName,
			VolumePct: linearToPct(n.volumeLinear),
			Mute:      n.mute,
		})
	}
	sort.Slice(res, func(i, j int) bool { return res[i].ID < res[j].ID })
	return res, nil
}

func (c *Client) SetStreamVolume(ctx context.Context, id uint32, pct int) error {
	return setVolumeNative(id, pct)
}

func (c *Client) SetStreamMute(ctx context.Context, id uint32, mute bool) error {
	return setMuteNative(id, mute)
}

func (c *Client) SetSinkVolume(ctx context.Context, id uint32, pct int) error {
	return setVolumeNative(id, pct)
}

func (c *Client) SetSinkMute(ctx context.Context, id uint32, mute bool) error {
	return setMuteNative(id, mute)
}

func (c *Client) SetDefaultSink(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("name required")
	}
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	if rc := int(C.nekonopaw_set_default_sink(cn)); rc != 0 {
		return fmt.Errorf("nekonopaw_set_default_sink: %d", rc)
	}
	return nil
}

func setVolumeNative(id uint32, pct int) error {
	if pct < 0 {
		return errors.New("pct must be >= 0")
	}
	linear := pctToLinear(pct)
	if rc := int(C.nekonopaw_set_volume(C.uint32_t(id), C.float(linear))); rc != 0 {
		return fmt.Errorf("nekonopaw_set_volume: %d", rc)
	}
	return nil
}

func setMuteNative(id uint32, mute bool) error {
	mi := C.int(0)
	if mute {
		mi = 1
	}
	if rc := int(C.nekonopaw_set_mute(C.uint32_t(id), mi)); rc != 0 {
		return fmt.Errorf("nekonopaw_set_mute: %d", rc)
	}
	return nil
}

// volume scale 変換
func linearToPct(linear float32) int {
	if linear <= 0 {
		return 0
	}
	pct := math.Cbrt(float64(linear)) * 100
	if pct > 200 {
		pct = 200
	}
	return int(math.Round(pct))
}

func pctToLinear(pct int) float32 {
	if pct <= 0 {
		return 0
	}
	n := float64(pct) / 100.0
	return float32(n * n * n)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// ----- C → Go callbacks (//export) -----

//export goOnGlobalNode
func goOnGlobalNode(id C.uint32_t, mediaClass *C.char) {
	c := getClient()
	if c == nil {
		return
	}
	mc := ""
	if mediaClass != nil {
		mc = C.GoString(mediaClass)
	}
	c.mu.Lock()
	c.nodes[uint32(id)] = &nodeState{id: uint32(id), mediaClass: mc}
	c.mu.Unlock()
}

//export goOnGlobalRemove
func goOnGlobalRemove(id C.uint32_t) {
	c := getClient()
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.nodes, uint32(id))
	c.mu.Unlock()
}

//export goOnNodeInfo
func goOnNodeInfo(id C.uint32_t, dict *C.struct_spa_dict) {
	c := getClient()
	if c == nil || dict == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.nodes[uint32(id)]
	if !ok {
		return
	}
	n.known = true
	// 空文字で上書きしない (pipewire restart 合間の partial info event で
	// 既存値を消す事故を踏んだ)。空でない値だけ採用、空はスキップ。
	setIfNonEmpty := func(target *string, v string) {
		if v != "" {
			*target = v
		}
	}
	setIfNonEmpty(&n.nodeName, lookupDict(dict, "node.name"))
	setIfNonEmpty(&n.description, firstNonEmpty(
		lookupDict(dict, "device.description"),
		lookupDict(dict, "node.description"),
		lookupDict(dict, "node.nick"),
	))
	setIfNonEmpty(&n.appName, lookupDict(dict, "application.name"))
	setIfNonEmpty(&n.appBinary, lookupDict(dict, "application.process.binary"))
	setIfNonEmpty(&n.mediaName, lookupDict(dict, "media.name"))
	setIfNonEmpty(&n.state, lookupDict(dict, "node.state"))
}

//export goOnNodeParam
func goOnNodeParam(id C.uint32_t, mute C.int, nVolumes C.int, volumes *C.float) {
	c := getClient()
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.nodes[uint32(id)]
	if !ok {
		return
	}
	if mute >= 0 {
		n.mute = mute != 0
	}
	if nVolumes > 0 && volumes != nil {
		s := unsafe.Slice((*float32)(unsafe.Pointer(volumes)), int(nVolumes))
		var sum float32
		for _, v := range s {
			sum += v
		}
		n.volumeLinear = sum / float32(len(s))
	}
}

//export goOnDefaultSinkChanged
func goOnDefaultSinkChanged(value *C.char) {
	c := getClient()
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if value == nil {
		c.defaultSink = ""
		return
	}
	raw := C.GoString(value)
	var v struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err == nil {
		c.defaultSink = v.Name
	}
}

func getClient() *Client {
	clientMu.Lock()
	defer clientMu.Unlock()
	return globalClient
}

func lookupDict(dict *C.struct_spa_dict, key string) string {
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))
	cv := C.nekonopaw_dict_lookup(dict, ck)
	if cv == nil {
		return ""
	}
	return C.GoString(cv)
}
