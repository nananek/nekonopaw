# nekonopaw

PipeWire のアプリ別 / デバイス別 出力音量ミキサーを web UI で操作する小さい
daemon。pwvucontrol を画面で見れない場面 (配信中 / 別端末から触りたい) で
使う想定。Tailscale 内 IP に bind して別端末 (iPhone/Mac/iPad/etc.) から
操作する。

## 機能

- **Streams (sink-inputs)**: アプリ毎の出力ストリーム一覧、音量スライダー
  (0〜100%、boost 不要)、mute toggle
- **Sinks (出力デバイス)**: 出力先一覧、master 音量、mute、default sink
  切替
- **PWA**: web app manifest + service worker で iOS/Android のホームに
  「アプリとして追加」できる
- 3 秒間隔の auto-refresh、slider 操作中は refresh 抑止

## 構成

- Go (1.24+) + 標準 `net/http`、frontend は vanilla JS + minimal CSS。
  embed.FS で binary に同梱
- Backend は **libpipewire native (cgo)**。`pw_thread_loop` で daemon に接続
  し、registry events で Audio/Sink + Stream/Output/Audio Node を track、
  Props param 購読で volume/mute を観測。"default" Metadata で default sink
  を track。Set 系も `pw_node_set_param` / `pw_metadata_set_property` を
  cgo で直接叩く (pactl shellout 不使用)
- HTTP read は in-memory cache から即返す (~1ms)。pactl 経路 (~150ms) より
  > 100x 速い
- C glue は `internal/pw/pw_glue.{c,h}`、Go 側 cgo + //export は
  `internal/pw/pw.go`
- volume scale: PulseAudio 系 UI と感覚を揃えるため、UI percent ↔ linear
  amplitude は cube root mapping (slider 50% = linear 0.125)

## build

```sh
sudo apt install -y golang libpipewire-0.3-dev pkg-config
go build -o nekonopaw .
```

## run

```sh
# tailscale IP に bind する例 (`tailscale ip -4` で取得)
./nekonopaw -listen "$(tailscale ip -4):8731"
```

別端末から `http://<tailscale-ip>:8731/` を開く。Add to Home Screen で
PWA 化可能。

## API

| method | path                          | body                          | 用途              |
|--------|-------------------------------|-------------------------------|-------------------|
| GET    | `/api/health`                 |                               | health check      |
| GET    | `/api/streams`                |                               | sink-inputs 一覧  |
| GET    | `/api/sinks`                  |                               | sinks 一覧        |
| PUT    | `/api/streams/{id}/volume`    | `{"volume_pct":100}`          | stream volume     |
| PUT    | `/api/streams/{id}/mute`      | `{"mute":true}`               | stream mute       |
| PUT    | `/api/sinks/{id}/volume`      | `{"volume_pct":100}`          | sink master volume|
| PUT    | `/api/sinks/{id}/mute`        | `{"mute":true}`               | sink mute         |
| PUT    | `/api/default-sink`           | `{"name":"alsa_output.xxx"}`  | default sink 切替 |

## TODO

- [ ] WebSocket で push 更新 (現状 3s polling)
- [ ] Sources (入力デバイス) を含める (mic 等)
- [ ] sink-input → sink の routing 表示 (pw_link を追加で track)
- [ ] node info の state (RUNNING/IDLE/SUSPENDED) を反映 (現在 props には
  入らないので pw_node_info の `state` field を別経路で拾う必要)
- [ ] 認証 (現状 Tailscale 内なら誰でも触れる)
