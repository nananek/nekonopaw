// libpipewire native backend の C glue。
//
// 役割:
//   - pw_thread_loop で daemon に常時接続、registry の global / global_remove
//     を購読
//   - Audio/Sink および Stream/Output/Audio の Node を bind し、info + Props
//     param を購読。受信 event は //export Go 関数経由で Go 側 cache に流す
//   - "default" Metadata を bind し、default.audio.sink プロパティを購読
//   - set_volume / set_mute / set_default_sink は Go 側から呼ばれ、適切な
//     pw_node_set_param / pw_metadata_set_property で実装

#include "pw_glue.h"
#include "_cgo_export.h"

#include <pipewire/pipewire.h>
#include <pipewire/extensions/metadata.h>
#include <spa/pod/parser.h>
#include <spa/pod/builder.h>
#include <spa/utils/dict.h>
#include <spa/param/props.h>

#include <stdlib.h>
#include <string.h>
#include <stdio.h>

// ------------------------------------------------------------
// state
// ------------------------------------------------------------

static struct pw_thread_loop *g_loop = NULL;
static struct pw_context     *g_context = NULL;
static struct pw_core        *g_core = NULL;
static struct pw_registry    *g_registry = NULL;
static struct spa_hook        g_registry_listener;

struct nekonopaw_node {
	struct nekonopaw_node *next;
	uint32_t id;
	struct pw_proxy *proxy;
	struct spa_hook listener;
	int n_channels; // 直近の param event で観測した channel 数
};
static struct nekonopaw_node *g_nodes_head = NULL;

static struct pw_proxy *g_metadata_proxy = NULL;
static struct spa_hook  g_metadata_listener;

// ------------------------------------------------------------
// node list helpers
// ------------------------------------------------------------

static struct nekonopaw_node *node_find(uint32_t id) {
	for (struct nekonopaw_node *n = g_nodes_head; n; n = n->next) {
		if (n->id == id) return n;
	}
	return NULL;
}

static void node_destroy_locked(struct nekonopaw_node *n) {
	if (n->proxy) {
		spa_hook_remove(&n->listener);
		pw_proxy_destroy(n->proxy);
	}
	free(n);
}

static void node_remove(uint32_t id) {
	struct nekonopaw_node **pp = &g_nodes_head;
	while (*pp) {
		if ((*pp)->id == id) {
			struct nekonopaw_node *target = *pp;
			*pp = target->next;
			node_destroy_locked(target);
			return;
		}
		pp = &(*pp)->next;
	}
}

// ------------------------------------------------------------
// dict lookup helper exposed to Go
// ------------------------------------------------------------

const char *nekonopaw_dict_lookup(const struct spa_dict *d, const char *key) {
	if (!d || !key) return NULL;
	return spa_dict_lookup(d, key);
}

// ------------------------------------------------------------
// node events
// ------------------------------------------------------------

static void on_node_info(void *data, const struct pw_node_info *info) {
	struct nekonopaw_node *n = (struct nekonopaw_node *)data;
	// 構造体のうち Go が使う部分は props のみ (media.class, node.name, etc.)
	// goOnNodeInfo は //export 越しの Go 関数。dict は callback 中だけ valid
	// なので、Go 側は同期的に必要な値を抜く。
	goOnNodeInfo((uint32_t)n->id, (struct spa_dict *)info->props);
}

static void on_node_param(void *data, int seq, uint32_t param_id,
			  uint32_t index, uint32_t next, const struct spa_pod *param) {
	(void)seq; (void)index; (void)next;
	struct nekonopaw_node *n = (struct nekonopaw_node *)data;
	if (param_id != SPA_PARAM_Props || !param) return;

	int mute = -1;       // -1 = unknown / unchanged
	float volumes[16];
	int n_volumes = 0;

	struct spa_pod_prop *prop;
	const struct spa_pod_object *obj = (const struct spa_pod_object *)param;
	SPA_POD_OBJECT_FOREACH(obj, prop) {
		switch (prop->key) {
		case SPA_PROP_mute: {
			bool b = false;
			if (spa_pod_get_bool(&prop->value, &b) == 0)
				mute = b ? 1 : 0;
			break;
		}
		case SPA_PROP_channelVolumes: {
			if (prop->value.type != SPA_TYPE_Array) break;
			const struct spa_pod_array *arr = (const struct spa_pod_array *)&prop->value;
			if (SPA_POD_ARRAY_VALUE_TYPE(arr) != SPA_TYPE_Float) break;
			uint32_t cnt = SPA_POD_ARRAY_N_VALUES(arr);
			float *vals = (float *)SPA_POD_ARRAY_VALUES(arr);
			n_volumes = cnt > 16 ? 16 : (int)cnt;
			for (int i = 0; i < n_volumes; i++) volumes[i] = vals[i];
			break;
		}
		default:
			break;
		}
	}

	if (n_volumes > 0) n->n_channels = n_volumes;

	goOnNodeParam((uint32_t)n->id,
		      mute,
		      n_volumes,
		      n_volumes > 0 ? &volumes[0] : NULL);
}

static const struct pw_node_events node_events = {
	.version = PW_VERSION_NODE_EVENTS,
	.info = on_node_info,
	.param = on_node_param,
};

// ------------------------------------------------------------
// metadata events
// ------------------------------------------------------------

static int on_metadata_property(void *data, uint32_t subject, const char *key,
				const char *type, const char *value) {
	(void)data;
	if (subject != PW_ID_CORE) return 0;
	if (!key) return 0;
	if (strcmp(key, "default.audio.sink") != 0) return 0;
	// value は SPA_TYPE_INFO_BASE "Spa:String:JSON" で {"name":"..."} 形式。
	// Go 側で JSON として parse する。NULL は default 解除を意味する。
	goOnDefaultSinkChanged((char *)value);
	return 0;
}

static const struct pw_metadata_events metadata_events = {
	.version = PW_VERSION_METADATA_EVENTS,
	.property = on_metadata_property,
};

// ------------------------------------------------------------
// registry events
// ------------------------------------------------------------

static void on_registry_global(void *data, uint32_t id, uint32_t permissions,
			       const char *type, uint32_t version,
			       const struct spa_dict *props) {
	(void)data; (void)permissions;

	if (strcmp(type, PW_TYPE_INTERFACE_Node) == 0) {
		// Node のうち Audio/Sink および Stream/Output/Audio のみ bind。
		const char *mc = props ? spa_dict_lookup(props, PW_KEY_MEDIA_CLASS) : NULL;
		if (!mc) return;
		if (strcmp(mc, "Audio/Sink") != 0 &&
		    strcmp(mc, "Stream/Output/Audio") != 0) return;

		struct nekonopaw_node *n = calloc(1, sizeof(*n));
		n->id = id;
		n->n_channels = 2;

		struct pw_proxy *proxy = pw_registry_bind(g_registry, id,
							  type, version,
							  0);
		if (!proxy) { free(n); return; }
		n->proxy = proxy;

		pw_node_add_listener((struct pw_node *)proxy, &n->listener,
				     &node_events, n);

		// Props param を購読。最初の info / param が直後に飛んでくる。
		uint32_t params[] = { SPA_PARAM_Props };
		pw_node_subscribe_params((struct pw_node *)proxy,
					 params,
					 SPA_N_ELEMENTS(params));

		n->next = g_nodes_head;
		g_nodes_head = n;

		goOnGlobalNode((uint32_t)id, (char *)mc);
		return;
	}

	if (strcmp(type, PW_TYPE_INTERFACE_Metadata) == 0) {
		const char *name = props ? spa_dict_lookup(props, "metadata.name") : NULL;
		if (!name || strcmp(name, "default") != 0) return;
		if (g_metadata_proxy) return; // 既に bind 済み

		struct pw_proxy *proxy = pw_registry_bind(g_registry, id,
							  type, version, 0);
		if (!proxy) return;
		g_metadata_proxy = proxy;
		pw_metadata_add_listener((struct pw_metadata *)proxy,
					 &g_metadata_listener,
					 &metadata_events, NULL);
	}
}

static void on_registry_global_remove(void *data, uint32_t id) {
	(void)data;
	node_remove(id);
	goOnGlobalRemove((uint32_t)id);
}

static const struct pw_registry_events registry_events = {
	.version = PW_VERSION_REGISTRY_EVENTS,
	.global = on_registry_global,
	.global_remove = on_registry_global_remove,
};

// ------------------------------------------------------------
// lifecycle
// ------------------------------------------------------------

void nekonopaw_init(void) {
	int argc = 0;
	pw_init(&argc, NULL);
}

void nekonopaw_deinit(void) {
	pw_deinit();
}

int nekonopaw_connect(void) {
	g_loop = pw_thread_loop_new("nekonopaw", NULL);
	if (!g_loop) return -1;

	if (pw_thread_loop_start(g_loop) < 0) {
		pw_thread_loop_destroy(g_loop);
		g_loop = NULL;
		return -2;
	}

	pw_thread_loop_lock(g_loop);

	g_context = pw_context_new(pw_thread_loop_get_loop(g_loop), NULL, 0);
	if (!g_context) goto fail;

	g_core = pw_context_connect(g_context, NULL, 0);
	if (!g_core) goto fail;

	g_registry = pw_core_get_registry(g_core, PW_VERSION_REGISTRY, 0);
	if (!g_registry) goto fail;

	pw_registry_add_listener(g_registry, &g_registry_listener,
				 &registry_events, NULL);

	pw_thread_loop_unlock(g_loop);
	return 0;

fail:
	if (g_registry) { pw_proxy_destroy((struct pw_proxy *)g_registry); g_registry = NULL; }
	if (g_core)     { pw_core_disconnect(g_core); g_core = NULL; }
	if (g_context)  { pw_context_destroy(g_context); g_context = NULL; }
	pw_thread_loop_unlock(g_loop);
	pw_thread_loop_stop(g_loop);
	pw_thread_loop_destroy(g_loop);
	g_loop = NULL;
	return -3;
}

void nekonopaw_disconnect(void) {
	if (!g_loop) return;

	pw_thread_loop_lock(g_loop);

	while (g_nodes_head) {
		struct nekonopaw_node *n = g_nodes_head;
		g_nodes_head = n->next;
		node_destroy_locked(n);
	}

	if (g_metadata_proxy) {
		spa_hook_remove(&g_metadata_listener);
		pw_proxy_destroy(g_metadata_proxy);
		g_metadata_proxy = NULL;
	}

	if (g_registry) {
		spa_hook_remove(&g_registry_listener);
		pw_proxy_destroy((struct pw_proxy *)g_registry);
		g_registry = NULL;
	}

	if (g_core) {
		pw_core_disconnect(g_core);
		g_core = NULL;
	}

	if (g_context) {
		pw_context_destroy(g_context);
		g_context = NULL;
	}

	pw_thread_loop_unlock(g_loop);
	pw_thread_loop_stop(g_loop);
	pw_thread_loop_destroy(g_loop);
	g_loop = NULL;
}

// ------------------------------------------------------------
// commands
// ------------------------------------------------------------

int nekonopaw_set_volume(uint32_t id, float linear_vol) {
	if (!g_loop) return -1;
	pw_thread_loop_lock(g_loop);
	struct nekonopaw_node *n = node_find(id);
	if (!n || !n->proxy) { pw_thread_loop_unlock(g_loop); return -2; }

	int n_ch = n->n_channels > 0 ? n->n_channels : 2;
	if (n_ch > 16) n_ch = 16;
	float volumes[16];
	for (int i = 0; i < n_ch; i++) volumes[i] = linear_vol;

	uint8_t buffer[1024];
	struct spa_pod_builder b = SPA_POD_BUILDER_INIT(buffer, sizeof(buffer));
	struct spa_pod *pod = spa_pod_builder_add_object(&b,
		SPA_TYPE_OBJECT_Props, SPA_PARAM_Props,
		SPA_PROP_channelVolumes,
			SPA_POD_Array(sizeof(float), SPA_TYPE_Float, n_ch, volumes));

	pw_node_set_param((struct pw_node *)n->proxy, SPA_PARAM_Props, 0, pod);
	pw_thread_loop_unlock(g_loop);
	return 0;
}

int nekonopaw_set_mute(uint32_t id, int mute) {
	if (!g_loop) return -1;
	pw_thread_loop_lock(g_loop);
	struct nekonopaw_node *n = node_find(id);
	if (!n || !n->proxy) { pw_thread_loop_unlock(g_loop); return -2; }

	uint8_t buffer[256];
	struct spa_pod_builder b = SPA_POD_BUILDER_INIT(buffer, sizeof(buffer));
	struct spa_pod *pod = spa_pod_builder_add_object(&b,
		SPA_TYPE_OBJECT_Props, SPA_PARAM_Props,
		SPA_PROP_mute, SPA_POD_Bool(mute ? true : false));

	pw_node_set_param((struct pw_node *)n->proxy, SPA_PARAM_Props, 0, pod);
	pw_thread_loop_unlock(g_loop);
	return 0;
}

int nekonopaw_set_default_sink(const char *node_name) {
	if (!g_loop) return -1;
	pw_thread_loop_lock(g_loop);
	if (!g_metadata_proxy) { pw_thread_loop_unlock(g_loop); return -2; }

	char value[512];
	snprintf(value, sizeof(value), "{\"name\":\"%s\"}", node_name);
	pw_metadata_set_property((struct pw_metadata *)g_metadata_proxy,
				 PW_ID_CORE,
				 "default.configured.audio.sink",
				 "Spa:String:JSON",
				 value);

	pw_thread_loop_unlock(g_loop);
	return 0;
}
