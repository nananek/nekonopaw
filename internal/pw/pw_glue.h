#ifndef NEKONOPAW_PW_GLUE_H
#define NEKONOPAW_PW_GLUE_H

#include <pipewire/pipewire.h>
#include <stdint.h>

// ライフサイクル
void nekonopaw_init(void);
void nekonopaw_deinit(void);
int  nekonopaw_connect(void);
void nekonopaw_disconnect(void);

// 操作系。失敗時は < 0。
// volume は linear amplitude (0.0..1.0)、PulseAudio の cube-root 表示には
// Go 側で変換する。
int nekonopaw_set_volume(uint32_t id, float linear_vol);
int nekonopaw_set_mute(uint32_t id, int mute);
int nekonopaw_set_default_sink(const char *node_name);

// spa_dict から指定 key を引く helper (string 返り、見つからなければ NULL)。
// _cgo の都合で関数化しておく (spa_dict_lookup は inline)。
const char *nekonopaw_dict_lookup(const struct spa_dict *d, const char *key);

#endif
