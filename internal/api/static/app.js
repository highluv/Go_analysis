'use strict';

// ─── Colour palette (cycled by namespace) ─────────────────────────────────
const PALETTE = [
  '#4e79a7','#f28e2b','#e15759','#76b7b2',
  '#59a14f','#edc948','#b07aa1','#9c755f',
];

function nsColor(ns, map) {
  if (!map[ns]) map[ns] = PALETTE[Object.keys(map).length % PALETTE.length];
  return map[ns];
}

// ─── API ──────────────────────────────────────────────────────────────────
const api = {
  async _req(method, url, body) {
    const opts = { method, headers: { 'Content-Type': 'application/json' } };
    if (body !== undefined) opts.body = JSON.stringify(body);
    const r = await fetch(url, opts);
    const data = await r.json().catch(() => ({ error: r.statusText }));
    if (!r.ok) throw new Error(data.error || r.statusText);
    return data;
  },
  listSnapshots:         ()            => api._req('GET',  '/api/v1/snapshots'),
  createSnapshot:        (name)        => api._req('POST', '/api/v1/snapshots', { name }),
  getSnapshot:           (id)          => api._req('GET',  `/api/v1/snapshots/${id}`),
  listRuns:              (snapId)      => api._req('GET',  `/api/v1/snapshots/${snapId}/runs`),
  getWorkloads:          (snapId)      => api._req('GET',  `/api/v1/snapshots/${snapId}/workloads`),
  analyze:               (snapId, sc)  => api._req('POST', `/api/v1/snapshots/${snapId}/analyze`, { scope: sc }),
  getRun:                (id)          => api._req('GET',  `/api/v1/runs/${id}`),
  getEdges:              (id)          => api._req('GET',  `/api/v1/runs/${id}/edges`),
};

// ─── Cytoscape instance (kept to destroy on re-render) ────────────────────
let cy = null;

// ─── Router ───────────────────────────────────────────────────────────────
async function route() {
  const hash = location.hash || '#/';
  const app  = document.getElementById('app');
  showSpinner(app);

  try {
    let m;
    if ((m = hash.match(/^#\/runs\/(\d+)$/)))        await renderRun(app, +m[1]);
    else if ((m = hash.match(/^#\/snapshots\/(\d+)$/))) await renderSnapshot(app, +m[1]);
    else                                                 await renderSnapshots(app);
  } catch (e) {
    app.innerHTML = `<div class="alert alert-danger mt-3"><strong>Ошибка:</strong> ${esc(e.message)}</div>`;
  }
}

window.addEventListener('hashchange', route);
window.addEventListener('load', route);

// ─── Page: Snapshots list ─────────────────────────────────────────────────
async function renderSnapshots(app) {
  const snaps = await api.listSnapshots();

  app.innerHTML = `
    <div class="d-flex align-items-center page-header">
      <h4 class="mb-0 flex-grow-1">Снапшоты</h4>
      <button class="btn btn-primary btn-sm" id="btn-new">+ Новый снапшот</button>
    </div>

    <div id="create-form" class="card mb-4 d-none">
      <div class="card-body d-flex gap-2 align-items-end flex-wrap">
        <div>
          <label class="form-label mb-1 small">Название</label>
          <input id="snap-name" class="form-control form-control-sm" value="snapshot">
        </div>
        <button class="btn btn-success btn-sm" id="btn-submit-snap">Создать</button>
        <button class="btn btn-outline-secondary btn-sm" id="btn-cancel-snap">Отмена</button>
      </div>
    </div>

    <div class="card shadow-sm">
      <div class="table-responsive">
        <table class="table table-hover align-middle mb-0">
          <thead class="table-light">
            <tr>
              <th style="width:60px">ID</th>
              <th>Название</th>
              <th>Источник</th>
              <th>Статус</th>
              <th>Создан</th>
            </tr>
          </thead>
          <tbody id="snaps-body">
            ${snaps.length === 0
              ? '<tr><td colspan="5" class="text-center text-muted py-4">Снапшотов пока нет — создайте первый</td></tr>'
              : snaps.map(s => `
                <tr class="clickable" data-id="${s.id}">
                  <td class="text-muted">${s.id}</td>
                  <td><strong>${esc(s.name)}</strong></td>
                  <td><code class="small">${esc(s.sourceType)}</code></td>
                  <td>${statusBadge(s.status)}</td>
                  <td class="text-muted small">${fmtDate(s.createdAt)}</td>
                </tr>`).join('')}
          </tbody>
        </table>
      </div>
    </div>
  `;

  document.getElementById('btn-new').onclick = () =>
    document.getElementById('create-form').classList.remove('d-none');

  document.getElementById('btn-cancel-snap').onclick = () =>
    document.getElementById('create-form').classList.add('d-none');

  document.getElementById('btn-submit-snap').onclick = async () => {
    const name = document.getElementById('snap-name').value.trim() || 'snapshot';
    try {
      await api.createSnapshot(name);
      route();
    } catch (e) { alert('Ошибка: ' + e.message); }
  };

  document.querySelectorAll('#snaps-body tr.clickable').forEach(tr =>
    tr.addEventListener('click', () => { location.hash = `#/snapshots/${tr.dataset.id}`; })
  );
}

// ─── Page: Snapshot detail ────────────────────────────────────────────────
async function renderSnapshot(app, id) {
  const [snap, runs] = await Promise.all([api.getSnapshot(id), api.listRuns(id)]);

  app.innerHTML = `
    ${breadcrumb([['#/', 'Снапшоты'], [null, `${esc(snap.name)} #${snap.id}`]])}

    <div class="row g-3 mb-4">
      <div class="col-md-5 col-lg-4">
        <div class="stat-box h-100">
          <div class="text-muted small mb-2 fw-semibold text-uppercase">Снапшот</div>
          <table class="table table-sm table-borderless mb-0">
            <tr><td class="text-muted pe-3">ID</td><td>${snap.id}</td></tr>
            <tr><td class="text-muted pe-3">Название</td><td>${esc(snap.name)}</td></tr>
            <tr><td class="text-muted pe-3">Источник</td><td><code>${esc(snap.sourceType)}</code></td></tr>
            <tr><td class="text-muted pe-3">Статус</td><td>${statusBadge(snap.status)}</td></tr>
            <tr><td class="text-muted pe-3">Создан</td><td class="small">${fmtDate(snap.createdAt)}</td></tr>
          </table>
        </div>
      </div>
      <div class="col-md-7 col-lg-8">
        <div class="stat-box h-100">
          <div class="text-muted small mb-2 fw-semibold text-uppercase">Запустить анализ</div>
          <div class="d-flex gap-2 flex-wrap align-items-end">
            <div>
              <label class="form-label mb-1 small">Область (scope)</label>
              <select class="form-select form-select-sm" id="scope-sel" style="min-width:200px">
                <option value="cluster">cluster</option>
                <option value="namespace:shop">namespace:shop</option>
                <option value="namespace:external">namespace:external</option>
                <option value="__custom">Указать вручную...</option>
              </select>
            </div>
            <div id="custom-wrap" class="d-none">
              <label class="form-label mb-1 small">Значение</label>
              <input id="custom-scope" class="form-control form-control-sm"
                     placeholder="namespace:X  или  workload:X/Y">
            </div>
            <button class="btn btn-primary btn-sm" id="btn-analyze">Запустить</button>
          </div>
          <div id="analyze-err" class="text-danger small mt-2"></div>
        </div>
      </div>
    </div>

    <h5 class="mb-2">История запусков</h5>
    <div class="card shadow-sm">
      <div class="table-responsive">
        <table class="table table-hover align-middle mb-0">
          <thead class="table-light">
            <tr>
              <th style="width:60px">ID</th>
              <th>Область</th>
              <th>Статус</th>
              <th>Создан</th>
              <th></th>
            </tr>
          </thead>
          <tbody id="runs-body">
            ${runs.length === 0
              ? '<tr><td colspan="5" class="text-center text-muted py-4">Запусков пока нет</td></tr>'
              : runs.map(r => `
                <tr>
                  <td class="text-muted">${r.id}</td>
                  <td><code class="small">${esc(r.scope)}</code></td>
                  <td>${statusBadge(r.status)}</td>
                  <td class="text-muted small">${fmtDate(r.createdAt)}</td>
                  <td><a href="#/runs/${r.id}" class="btn btn-sm btn-outline-primary">Открыть →</a></td>
                </tr>`).join('')}
          </tbody>
        </table>
      </div>
    </div>

    <!-- Evidence modal (reused from run page) -->
    <div class="modal fade" id="ev-modal" tabindex="-1">
      <div class="modal-dialog"><div class="modal-content">
        <div class="modal-header">
          <h5 class="modal-title">Evidence</h5>
          <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
        </div>
        <div class="modal-body" id="ev-modal-body"></div>
      </div></div>
    </div>
  `;

  document.getElementById('scope-sel').onchange = function () {
    document.getElementById('custom-wrap').classList.toggle('d-none', this.value !== '__custom');
  };

  document.getElementById('btn-analyze').onclick = async () => {
    const sel = document.getElementById('scope-sel').value;
    const scope = sel === '__custom'
      ? document.getElementById('custom-scope').value.trim()
      : sel;
    if (!scope) { document.getElementById('analyze-err').textContent = 'Введите scope'; return; }
    document.getElementById('analyze-err').textContent = '';
    try {
      const run = await api.analyze(id, scope);
      location.hash = `#/runs/${run.id}`;
    } catch (e) {
      document.getElementById('analyze-err').textContent = 'Ошибка: ' + e.message;
    }
  };
}

// ─── Page: Run detail ─────────────────────────────────────────────────────
async function renderRun(app, id) {
  const [run, edgesData] = await Promise.all([api.getRun(id), api.getEdges(id)]);
  const edges = edgesData.edges || [];
  const workloads = await api.getWorkloads(run.snapshotId);
  const wlMap = Object.fromEntries(workloads.map(w => [w.name, w]));

  if (cy) { cy.destroy(); cy = null; }

  app.innerHTML = `
    ${breadcrumb([
      ['#/', 'Снапшоты'],
      [`#/snapshots/${run.snapshotId}`, `Снапшот #${run.snapshotId}`],
      [null, `Запуск #${run.id}`],
    ])}

    <div class="d-flex align-items-center page-header flex-wrap gap-2">
      <div class="flex-grow-1">
        <h5 class="mb-0">Запуск #${run.id}</h5>
        <div class="text-muted small">
          Область: <code>${esc(run.scope)}</code> &nbsp;·&nbsp;
          ${statusBadge(run.status)} &nbsp;·&nbsp;
          Рёбер: <strong>${edgesData.count}</strong>
        </div>
      </div>
      <div class="btn-group btn-group-sm">
        <input type="radio" class="btn-check" name="vmode" id="vmode-graph" checked>
        <label class="btn btn-outline-primary" for="vmode-graph">Граф</label>
        <input type="radio" class="btn-check" name="vmode" id="vmode-table">
        <label class="btn btn-outline-primary" for="vmode-table">Таблица</label>
      </div>
    </div>

    <!-- Graph view -->
    <div id="view-graph">
      <div class="row g-3">
        <div class="col-xl-8 col-lg-7">
          <div id="cy"></div>
          <div id="ns-legend" class="ns-legend mt-2"></div>
        </div>
        <div class="col-xl-4 col-lg-5">
          <div class="card evidence-card shadow-sm h-100">
            <div class="card-body" id="evidence-panel">
              <p class="text-muted small mb-0">
                Нажмите на <strong>узел</strong> — увидите сервисы и порты.<br>
                Нажмите на <strong>ребро</strong> — увидите evidence.
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Table view -->
    <div id="view-table" class="d-none">
      <div class="card shadow-sm">
        <div class="table-responsive">
          <table class="table table-sm table-hover align-middle mb-0">
            <thead class="table-light">
              <tr>
                <th>Источник</th><th>Назначение</th>
                <th>Через сервис</th><th>Порт</th>
                <th>Протокол</th><th>Evidence</th>
              </tr>
            </thead>
            <tbody id="edges-tbody">
              ${edges.length === 0
                ? '<tr><td colspan="6" class="text-center text-muted py-4">Рёбер нет</td></tr>'
                : edges.map((e, i) => `
                  <tr class="clickable" data-idx="${i}">
                    <td><code class="small">${esc(e.source)}</code></td>
                    <td><code class="small">${esc(e.dest)}</code></td>
                    <td><code class="small">${esc(e.viaService || '—')}</code></td>
                    <td>${e.port || '—'}</td>
                    <td>${esc(e.protocol || '—')}</td>
                    <td>${evidenceSummary(e.evidence)}</td>
                  </tr>`).join('')}
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Evidence modal -->
    <div class="modal fade" id="ev-modal" tabindex="-1">
      <div class="modal-dialog"><div class="modal-content">
        <div class="modal-header">
          <h5 class="modal-title" id="ev-modal-title">Evidence</h5>
          <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
        </div>
        <div class="modal-body" id="ev-modal-body"></div>
      </div></div>
    </div>
  `;

  // View toggle
  document.getElementById('vmode-graph').onchange = () => {
    document.getElementById('view-graph').classList.remove('d-none');
    document.getElementById('view-table').classList.add('d-none');
  };
  document.getElementById('vmode-table').onchange = () => {
    document.getElementById('view-graph').classList.add('d-none');
    document.getElementById('view-table').classList.remove('d-none');
  };

  buildGraph(edges, wlMap);

  // Table row → modal
  document.querySelectorAll('#edges-tbody tr.clickable').forEach(tr => {
    tr.addEventListener('click', () => {
      const e = edges[+tr.dataset.idx];
      openEvidenceModal(`${e.source} → ${e.dest}`, e.evidence);
    });
  });
}

// ─── Cytoscape graph ──────────────────────────────────────────────────────
function buildGraph(edges, wlMap) {
  const nsMap = {};
  const nodeSet = new Set();
  const cyEdges = [];

  edges.forEach(e => {
    nodeSet.add(e.source);
    nodeSet.add(e.dest);
    cyEdges.push({
      data: {
        id: `${e.source}||${e.dest}`,
        source: e.source,
        target: e.dest,
        label: e.port ? String(e.port) : '',
        evidence: e.evidence,
        viaService: e.viaService || '',
      },
    });
  });

  const cyNodes = Array.from(nodeSet).map(wl => {
    const parts = wl.split('/');
    const ns    = parts[0] || wl;
    const name  = parts[1] || wl;
    return { data: { id: wl, label: name, ns, color: nsColor(ns, nsMap) } };
  });

  cy = cytoscape({
    container: document.getElementById('cy'),
    elements: { nodes: cyNodes, edges: cyEdges },
    style: [
      {
        selector: 'node',
        style: {
          'background-color':  'data(color)',
          'label':             'data(label)',
          'color':             '#fff',
          'text-valign':       'center',
          'text-halign':       'center',
          'font-size':         '11px',
          'font-weight':       '600',
          'width':             70,
          'height':            70,
          'text-wrap':         'wrap',
          'text-max-width':    62,
        },
      },
      {
        selector: 'edge',
        style: {
          'width':                   2,
          'line-color':              '#adb5bd',
          'target-arrow-color':      '#adb5bd',
          'target-arrow-shape':      'triangle',
          'curve-style':             'bezier',
          'label':                   'data(label)',
          'font-size':               '10px',
          'color':                   '#495057',
          'text-background-color':   '#fff',
          'text-background-opacity': 0.85,
          'text-background-padding': '2px',
        },
      },
      {
        selector: 'edge:selected',
        style: {
          'line-color':         '#0d6efd',
          'target-arrow-color': '#0d6efd',
          'width':              3,
        },
      },
      {
        selector: 'node:selected',
        style: { 'border-width': 3, 'border-color': '#0d6efd' },
      },
    ],
    layout: {
      name:          'cose',
      padding:        50,
      idealEdgeLength: 130,
      nodeOverlap:    20,
      animate:        false,
    },
  });

  // Node click → workload details
  cy.on('tap', 'node', evt => {
    const name = evt.target.data('id');
    const wl   = wlMap[name];
    const panel = document.getElementById('evidence-panel');
    panel.innerHTML = wl ? renderWorkloadInfo(wl) : `<p class="text-muted small mb-0">${esc(name)}</p>`;
  });

  // Edge click → evidence panel
  cy.on('tap', 'edge', evt => {
    const d = evt.target.data();
    const panel = document.getElementById('evidence-panel');
    panel.innerHTML = `
      <div class="fw-semibold mb-1">${esc(d.source)} → ${esc(d.target)}</div>
      ${d.viaService ? `<div class="text-muted small mb-2">Через: <code>${esc(d.viaService)}</code></div>` : ''}
      ${renderEvidence(d.evidence)}
    `;
  });

  // Background click → clear panel
  cy.on('tap', evt => {
    if (evt.target === cy) {
      document.getElementById('evidence-panel').innerHTML =
        '<p class="text-muted small mb-0">Нажмите на узел или ребро, чтобы увидеть детали</p>';
    }
  });

  // Namespace legend
  const legend = document.getElementById('ns-legend');
  Object.entries(nsMap).forEach(([ns, color]) => {
    const el = document.createElement('span');
    el.className = 'small text-muted';
    el.innerHTML = `<span class="ns-dot" style="background:${color}"></span>${esc(ns)}`;
    legend.appendChild(el);
  });
}

// ─── Evidence rendering ───────────────────────────────────────────────────
function renderEvidence(ev) {
  if (!ev || ev.length === 0) {
    return '<p class="text-muted small mb-0">Evidence отсутствует (default allow)</p>';
  }
  return ev.map(e => `
    <div class="evidence-item border rounded p-2 mb-2 bg-light">
      <span class="badge bg-secondary mb-1">${esc(e.matchedBy)}</span>
      ${e.policy            ? `<div><span class="text-muted">Политика:</span> <code>${esc(e.policy)}</code></div>` : ''}
      ${e.sourceServiceAccount ? `<div><span class="text-muted">SA:</span> <code>${esc(e.sourceServiceAccount)}</code></div>` : ''}
      ${e.matchedValue      ? `<div><span class="text-muted">Значение:</span> <code>${esc(e.matchedValue)}</code></div>` : ''}
      ${e.service           ? `<div><span class="text-muted">Сервис:</span> <code>${esc(e.service)}</code></div>` : ''}
    </div>`).join('');
}

function renderWorkloadInfo(wl) {
  const svcHtml = wl.services.length === 0
    ? '<p class="text-muted small mb-0">Сервисов нет</p>'
    : wl.services.map(s => {
        const portsHtml = s.ports.length === 0
          ? '<span class="text-muted small">портов нет</span>'
          : s.ports.map(p => `
              <div class="small font-monospace">
                <span class="badge bg-secondary">${esc(p.protocol)}</span>
                :${p.port}${p.targetPort ? ` → ${esc(p.targetPort)}` : ''}
                ${p.name ? `<span class="text-muted ms-1">(${esc(p.name)})</span>` : ''}
              </div>`).join('');
        return `
          <div class="border rounded p-2 mb-2 bg-light">
            <div class="fw-semibold small mb-1">
              <code>${esc(s.name)}</code>
              ${s.type ? `<span class="text-muted ms-1">${esc(s.type)}</span>` : ''}
            </div>
            ${portsHtml}
          </div>`;
      }).join('');

  return `
    <div class="fw-semibold mb-1">${esc(wl.name)}</div>
    <div class="text-muted small mb-1">
      <span class="badge bg-dark">${esc(wl.kind)}</span>
    </div>
    <div class="text-muted small mb-2">
      SA: <code>${esc(wl.serviceAccount)}</code>
    </div>
    <div class="text-muted small fw-semibold mb-1">Сервисы</div>
    ${svcHtml}
  `;
}

function openEvidenceModal(title, ev) {
  document.getElementById('ev-modal-title').textContent = title;
  document.getElementById('ev-modal-body').innerHTML = renderEvidence(ev);
  bootstrap.Modal.getOrCreateInstance(document.getElementById('ev-modal')).show();
}

// ─── UI helpers ───────────────────────────────────────────────────────────
function showSpinner(app) {
  if (cy) { cy.destroy(); cy = null; }
  app.innerHTML = '<div class="d-flex justify-content-center mt-5"><div class="spinner-border text-primary" role="status"></div></div>';
}

function breadcrumb(items) {
  const parts = items.map((item, i) => {
    const [href, label] = item;
    const isLast = i === items.length - 1;
    return isLast
      ? `<li class="breadcrumb-item active">${label}</li>`
      : `<li class="breadcrumb-item"><a href="${href}" class="text-decoration-none">${label}</a></li>`;
  }).join('');
  return `<nav aria-label="breadcrumb" class="mb-3"><ol class="breadcrumb">${parts}</ol></nav>`;
}

function statusBadge(s) {
  return `<span class="badge badge-${s}">${esc(s)}</span>`;
}

function evidenceSummary(ev) {
  if (!ev || ev.length === 0) return '<span class="text-muted small">default allow</span>';
  const types = [...new Set(ev.map(e => e.matchedBy))];
  return types.map(t => `<span class="badge bg-secondary me-1 small">${esc(t)}</span>`).join('');
}

function fmtDate(s) {
  if (!s) return '—';
  return new Date(s).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' });
}

function esc(s) {
  if (s == null) return '';
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
