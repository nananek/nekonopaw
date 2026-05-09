// 暫定 frontend。/api/streams と /api/sinks を叩いて表示するだけ。
// 編集 UI と auto-refresh は後で。

async function fetchJSON(path) {
  const r = await fetch(path);
  if (!r.ok) throw new Error(`${path}: ${r.status}`);
  return r.json();
}

async function refresh() {
  try {
    const streams = await fetchJSON('/api/streams');
    const ul = document.getElementById('stream-list');
    ul.innerHTML = streams.length === 0
      ? '<li>(none — backend not yet implemented)</li>'
      : streams.map(s => `<li>${JSON.stringify(s)}</li>`).join('');
  } catch (e) {
    document.getElementById('stream-list').innerHTML = `<li>error: ${e.message}</li>`;
  }
  try {
    const sinks = await fetchJSON('/api/sinks');
    const ul = document.getElementById('sink-list');
    ul.innerHTML = sinks.length === 0
      ? '<li>(none — backend not yet implemented)</li>'
      : sinks.map(s => `<li>${JSON.stringify(s)}</li>`).join('');
  } catch (e) {
    document.getElementById('sink-list').innerHTML = `<li>error: ${e.message}</li>`;
  }
}

refresh();
