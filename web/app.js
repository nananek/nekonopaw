// 画面 = sinks + streams のカード一覧。slider / mute / default 切替を出す。
// auto-refresh は 3 秒間隔。slider 操作中は refresh をスキップして UI 揺れを抑える。

const REFRESH_MS = 3000;
const VOL_MAX = 100; // boost 不要

let dragging = false;
let timer = null;

const els = {
  sinks: document.getElementById('sinks'),
  streams: document.getElementById('streams'),
  status: document.getElementById('status'),
  refresh: document.getElementById('refresh'),
};

async function fetchJSON(path, opts = {}) {
  const r = await fetch(path, opts);
  if (!r.ok) throw new Error(`${path}: ${r.status} ${await r.text()}`);
  return r.status === 204 ? null : r.json();
}

function setStatus(msg, isError = false) {
  els.status.textContent = msg;
  els.status.className = isError ? 'err' : '';
}

function renderCard({ id, title, subtitle, badge, volumePct, mute, onVolume, onMute, extra }) {
  const card = document.createElement('div');
  card.className = 'card' + (mute ? ' muted' : '');
  card.dataset.id = id;
  card.innerHTML = `
    <div class="card-head">
      <span class="title">${escapeHTML(title)}</span>
      ${badge ? `<span class="badge">${escapeHTML(badge)}</span>` : ''}
    </div>
    ${subtitle ? `<div class="subtitle">${escapeHTML(subtitle)}</div>` : ''}
    <div class="row">
      <button type="button" class="mute" aria-label="mute">${mute ? '🔇' : '🔊'}</button>
      <input type="range" min="0" max="${VOL_MAX}" value="${volumePct}" class="vol" aria-label="volume">
      <span class="pct">${volumePct}%</span>
    </div>
    ${extra ?? ''}
  `;
  const slider = card.querySelector('.vol');
  const pctLabel = card.querySelector('.pct');
  slider.addEventListener('input', () => {
    pctLabel.textContent = `${slider.value}%`;
    dragging = true;
  });
  slider.addEventListener('change', async () => {
    try { await onVolume(parseInt(slider.value, 10)); } catch (e) { setStatus(e.message, true); }
    dragging = false;
  });
  card.querySelector('.mute').addEventListener('click', async () => {
    try { await onMute(!mute); await refresh(); } catch (e) { setStatus(e.message, true); }
  });
  return card;
}

function escapeHTML(s) {
  return String(s ?? '').replace(/[&<>"']/g, c => ({ '&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;', "'":'&#39;' }[c]));
}

async function refresh() {
  if (dragging) return;
  try {
    const [sinks, streams] = await Promise.all([fetchJSON('/api/sinks'), fetchJSON('/api/streams')]);

    els.sinks.replaceChildren(...sinks.map(s => {
      const extra = `<div class="row"><label class="default-row">
        <input type="radio" name="default-sink" ${s.default ? 'checked' : ''} ${s.default ? 'disabled' : ''}>
        default sink
      </label></div>`;
      const card = renderCard({
        id: s.id,
        title: s.description || s.name,
        subtitle: s.name,
        badge: s.state,
        volumePct: s.volume_pct,
        mute: s.mute,
        onVolume: pct => fetchJSON(`/api/sinks/${s.id}/volume`, { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ volume_pct: pct }) }),
        onMute: mute => fetchJSON(`/api/sinks/${s.id}/mute`, { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ mute }) }),
        extra,
      });
      const radio = card.querySelector('input[type=radio]');
      radio.addEventListener('change', async () => {
        try { await fetchJSON('/api/default-sink', { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ name: s.name }) }); await refresh(); }
        catch (e) { setStatus(e.message, true); }
      });
      return card;
    }));

    els.streams.replaceChildren(...streams.map(s => {
      const subtitle = [s.app_binary, s.media_name].filter(Boolean).join(' · ');
      return renderCard({
        id: s.id,
        title: s.name || `(stream ${s.id})`,
        subtitle,
        badge: s.corked ? 'corked' : null,
        volumePct: s.volume_pct,
        mute: s.mute,
        onVolume: pct => fetchJSON(`/api/streams/${s.id}/volume`, { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ volume_pct: pct }) }),
        onMute: mute => fetchJSON(`/api/streams/${s.id}/mute`, { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ mute }) }),
      });
    }));

    setStatus(`updated ${new Date().toLocaleTimeString()}  ·  sinks: ${sinks.length}  streams: ${streams.length}`);
  } catch (e) {
    setStatus(e.message, true);
  }
}

els.refresh.addEventListener('click', refresh);
refresh();
timer = setInterval(refresh, REFRESH_MS);

if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(() => {/* offline 強化なので失敗は黙殺 */});
}
