// Package pw は libpipewire の薄い wrapper。
//
// 設計メモ:
//   - pw_thread_loop を使う (pipewire 推奨の library 用 main loop)。Go 側から
//     ロック取って context/core を呼び出す。callback は pw thread から飛ぶので
//     Go の handler 越しに channel に流して main goroutine 側で処理する想定。
//   - 現状はまだ event handler / object tracking / volume command 未実装。
//     pw_init + connect + disconnect だけが動く骨格。
package pw

/*
#cgo pkg-config: libpipewire-0.3
#include <pipewire/pipewire.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Init は libpipewire の global state 初期化。プロセス起動時に 1 回。
func Init() {
	var argc C.int = 0
	C.pw_init(&argc, nil)
}

// Deinit は libpipewire の global state 後始末。プロセス終了時に 1 回。
func Deinit() {
	C.pw_deinit()
}

// Client は pw_thread_loop + context + core 接続を保持する。
type Client struct {
	loop    *C.struct_pw_thread_loop
	context *C.struct_pw_context
	core    *C.struct_pw_core
}

// Connect は pipewire daemon に接続し、event 受信用の thread loop を回す。
func Connect() (*Client, error) {
	cName := C.CString("nekonopaw")
	defer C.free(unsafe.Pointer(cName))

	loop := C.pw_thread_loop_new(cName, nil)
	if loop == nil {
		return nil, errors.New("pw_thread_loop_new failed")
	}

	if rc := C.pw_thread_loop_start(loop); rc < 0 {
		C.pw_thread_loop_destroy(loop)
		return nil, errors.New("pw_thread_loop_start failed")
	}

	C.pw_thread_loop_lock(loop)
	defer C.pw_thread_loop_unlock(loop)

	context := C.pw_context_new(C.pw_thread_loop_get_loop(loop), nil, 0)
	if context == nil {
		C.pw_thread_loop_stop(loop)
		C.pw_thread_loop_destroy(loop)
		return nil, errors.New("pw_context_new failed")
	}

	core := C.pw_context_connect(context, nil, 0)
	if core == nil {
		C.pw_context_destroy(context)
		C.pw_thread_loop_stop(loop)
		C.pw_thread_loop_destroy(loop)
		return nil, errors.New("pw_context_connect failed (daemon down?)")
	}

	return &Client{loop: loop, context: context, core: core}, nil
}

// Close で接続を畳む。
func (c *Client) Close() {
	if c.loop == nil {
		return
	}
	C.pw_thread_loop_lock(c.loop)
	if c.core != nil {
		C.pw_core_disconnect(c.core)
		c.core = nil
	}
	if c.context != nil {
		C.pw_context_destroy(c.context)
		c.context = nil
	}
	C.pw_thread_loop_unlock(c.loop)
	C.pw_thread_loop_stop(c.loop)
	C.pw_thread_loop_destroy(c.loop)
	c.loop = nil
}
