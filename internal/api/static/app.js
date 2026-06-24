'use strict';

// ─── Human-readable labels for matchedBy constants ───────────────────────
const MATCH_LABELS = {
  'principal':             'SA / Principal',
  'namespace':             'Namespace',
  'any':                   'Любой источник',
  'default-allow-noallow': 'По умолчанию (нет политик)',
};

function matchLabel(raw) {
  return MATCH_LABELS[raw] || raw;
}

// ─── Namespaces hidden from the graph (platform / infrastructure) ────────
const HIDDEN_NS = new Set(['istio-system']);

function hiddenNs(qualifiedName) {
  const i = qualifiedName.indexOf('/');
  return i >= 0 && HIDDEN_NS.has(qualifiedName.slice(0, i));
}

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
  const [snap, runs, workloads] = await Promise.all([api.getSnapshot(id), api.listRuns(id), api.getWorkloads(id)]);

  const namespaces = [...new Set(workloads.map(w => {
    const i = w.name.indexOf('/');
    return i >= 0 ? w.name.slice(0, i) : w.name;
  }).filter(Boolean))].sort();
  const nsOptions = namespaces
    .map(ns => `<option value="namespace:${esc(ns)}">namespace:${esc(ns)}</option>`)
    .join('');

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
                ${nsOptions}
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
                Нажмите на <strong>воркло́д</strong> (○) — тип, SA, labels, образы, политики.<br>
                Нажмите на <strong>сервис</strong> (△) — тип, порты, selector.<br>
                Нажмите на <strong>ребро</strong> — сервис и политики.
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
// Mental model: Source workload → Service (△) → Destination workload
function buildGraph(edgesAll, wlMapAll) {
  const nsMap = {};

  // Drop platform namespaces (istio-system, etc.)
  const edges = edgesAll.filter(e => !hiddenNs(e.source) && !hiddenNs(e.dest));
  const wlMap = Object.fromEntries(Object.entries(wlMapAll).filter(([k]) => !hiddenNs(k)));

  // ── Indexes ─────────────────────────────────────────────────────────────
  const allWlNames = new Set(Object.keys(wlMap));
  edges.forEach(e => { allWlNames.add(e.source); allWlNames.add(e.dest); });

  const svcMap   = {};  // svcName → workloadServiceDTO
  const svcOwner = {};  // svcName → owning wlName
  Object.values(wlMap).forEach(wl => {
    (wl.services || []).forEach(svc => {
      svcMap[svc.name]   = svc;
      svcOwner[svc.name] = wl.name;
    });
  });

  // Workloads with ≥1 service get a compound parent (groups wl ○ + svc △)
  const wlHasSvc = new Set(
    Object.values(wlMap).filter(wl => (wl.services || []).length > 0).map(wl => wl.name)
  );

  // Pre-populate nsMap so compound nodes share the same colour
  allWlNames.forEach(wl => {
    const i  = wl.indexOf('/');
    nsColor(i >= 0 ? wl.slice(0, i) : wl, nsMap);
  });

  // ── Nodes ──────────────────────────────────────────────────────────────
  const cyNodes = [];

  // Compound containers
  wlHasSvc.forEach(wlName => {
    const i = wlName.indexOf('/');
    const ns = i >= 0 ? wlName.slice(0, i) : wlName;
    cyNodes.push({
      data: { id: `group:${wlName}`, nodeType: 'compound', color: nsMap[ns] || '#adb5bd' },
    });
  });

  // Workload nodes (inside compound when they have services)
  allWlNames.forEach(wl => {
    const i    = wl.indexOf('/');
    const ns   = i >= 0 ? wl.slice(0, i) : wl;
    const name = i >= 0 ? wl.slice(i + 1) : wl;
    const d    = { id: wl, label: name, ns, color: nsColor(ns, nsMap), nodeType: 'workload' };
    if (wlHasSvc.has(wl)) d.parent = `group:${wl}`;
    cyNodes.push({ data: d });
  });

  // Service nodes (inside compound of their owning workload)
  Object.values(svcMap).forEach(svc => {
    const i     = svc.name.indexOf('/');
    const ns    = i >= 0 ? svc.name.slice(0, i) : svc.name;
    const label = i >= 0 ? svc.name.slice(i + 1) : svc.name;
    const owner = svcOwner[svc.name];
    const d     = { id: `svc:${svc.name}`, label, ns, color: nsColor(ns, nsMap), nodeType: 'service' };
    if (owner && wlHasSvc.has(owner)) d.parent = `group:${owner}`;
    cyNodes.push({ data: d });
  });

  // ── Edges ──────────────────────────────────────────────────────────────
  // Single hop: source_wl → svc (△ is inside compound of dest_wl — grouping replaces svc→dest edge)
  const cyEdges = [];
  const srcSvcGroups = {};

  edges.forEach(e => {
    if (e.viaService) {
      const k = `${e.source}|${e.viaService}`;
      if (!srcSvcGroups[k]) {
        srcSvcGroups[k] = {
          src: e.source, via: e.viaService,
          destWl: svcOwner[e.viaService] || e.dest,
          conns: [],
        };
      }
      srcSvcGroups[k].conns.push(e);
    } else {
      // fallback: direct workload→workload
      cyEdges.push({
        data: {
          id: `direct:${e.source}|${e.dest}`,
          source: e.source, target: e.dest,
          edgeType: 'flow', connEdges: [e],
          destWl: e.dest, label: e.port ? String(e.port) : '',
        },
      });
    }
  });

  Object.entries(srcSvcGroups).forEach(([key, g]) => {
    const ports = [...new Set(g.conns.map(e => e.port).filter(Boolean))];
    cyEdges.push({
      data: {
        id: `flow:${key}`,
        source: g.src, target: `svc:${g.via}`,
        edgeType: 'flow', viaService: g.via,
        connEdges: g.conns, destWl: g.destWl,
        label: ports.join(','),
      },
    });
  });

  // Invisible spring edges inside each compound: pull svc △ toward its workload ○
  // so cose doesn't scatter them across the container (no visible edge, pure physics)
  Object.values(svcMap).forEach(svc => {
    const owner = svcOwner[svc.name];
    if (owner && allWlNames.has(owner)) {
      cyEdges.push({
        data: {
          id: `spring:${svc.name}`,
          source: `svc:${svc.name}`, target: owner,
          edgeType: 'spring',
        },
      });
    }
  });

  // ── Cytoscape init ──────────────────────────────────────────────────────
  cy = cytoscape({
    container: document.getElementById('cy'),
    elements:  { nodes: cyNodes, edges: cyEdges },
    style: [
      {
        selector: 'node[nodeType = "compound"]',
        style: {
          'background-color':   'data(color)',
          'background-opacity': 0.07,
          'border-color':       'data(color)',
          'border-width':       1.5,
          'border-style':       'dashed',
          'padding':            '10px',
          'label':              '',
        },
      },
      {
        selector: 'node[nodeType = "workload"]',
        style: {
          'shape':             'ellipse',
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
        selector: 'node[nodeType = "service"]',
        style: {
          'shape':              'triangle',
          'background-color':   'data(color)',
          'background-opacity': 0.65,
          'label':              'data(label)',
          'color':              '#333',
          'text-valign':        'bottom',
          'text-margin-y':      4,
          'text-halign':        'center',
          'font-size':          '10px',
          'width':              44,
          'height':             44,
        },
      },
      {
        selector: 'edge[edgeType = "flow"]',
        style: {
          'width':                   2,
          'line-color':              '#6c757d',
          'target-arrow-color':      '#6c757d',
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
        selector: 'edge[edgeType = "flow"]:selected',
        style: { 'line-color': '#0d6efd', 'target-arrow-color': '#0d6efd', 'width': 3 },
      },
      // Spring edges: invisible, only exist for layout physics
      {
        selector: 'edge[edgeType = "spring"]',
        style: { 'width': 0, 'opacity': 0, 'events': 'no' },
      },
      // Incoming edges highlighted green on workload click
      {
        selector: 'edge.hl-in',
        style: { 'line-color': '#198754', 'target-arrow-color': '#198754', 'width': 3 },
      },
      // Outgoing edges highlighted red on workload click
      {
        selector: 'edge.hl-out',
        style: { 'line-color': '#dc3545', 'target-arrow-color': '#dc3545', 'width': 3 },
      },
      {
        selector: 'node:selected',
        style: { 'border-width': 3, 'border-color': '#0d6efd' },
      },
    ],
    layout: {
      name:         'cose',
      animate:       false,
      padding:       60,
      // Pull compound children (svc △ + workload ○) tightly together
      nestingFactor: 0.1,
      // Strong repulsion keeps different workload groups from overlapping
      nodeRepulsion: 2048,
      // Short spring edges → service stays right next to its workload
      idealEdgeLength: edge => edge.data('edgeType') === 'spring' ? 10 : 15,
      nodeOverlap:   4,
      gravity:       2,
      numIter:       1500,
    },
  });

  // ── Helpers ─────────────────────────────────────────────────────────────
  const panel = () => document.getElementById('evidence-panel');

  function resetHighlight() { cy.edges().removeClass('hl-in hl-out'); }

  function showWorkload(wlName) {
    const wl      = wlMap[wlName];
    const inbound  = edges.filter(e => e.dest   === wlName);
    const outbound = edges.filter(e => e.source === wlName);
    resetHighlight();
    cy.edges('[edgeType="flow"]').forEach(e => {
      if (e.data('destWl') === wlName)      e.addClass('hl-in');
      else if (e.source().id() === wlName)  e.addClass('hl-out');
    });
    panel().innerHTML = renderWorkloadInfo(
      wl || { name: wlName, kind: '', serviceAccount: '', services: [], labels: {}, images: [] },
      inbound, outbound,
    );
  }

  // ── Click handlers ──────────────────────────────────────────────────────
  cy.on('tap', 'node[nodeType = "workload"]', evt => {
    showWorkload(evt.target.data('id'));
  });

  // Click on compound background → same as clicking the workload inside
  cy.on('tap', 'node[nodeType = "compound"]', evt => {
    showWorkload(evt.target.id().replace(/^group:/, ''));
  });

  cy.on('tap', 'node[nodeType = "service"]', evt => {
    resetHighlight();
    const svcName = evt.target.data('id').replace(/^svc:/, '');
    const svc     = svcMap[svcName];
    panel().innerHTML = svc
      ? renderServiceInfo(svc)
      : `<p class="text-muted small mb-0">${esc(svcName)}</p>`;
  });

  cy.on('tap', 'edge[edgeType = "flow"]', evt => {
    resetHighlight();
    const d = evt.target.data();
    panel().innerHTML = renderEdgeInfo(d.viaService || '', d.connEdges || []);
  });

  cy.on('tap', evt => {
    if (evt.target === cy) {
      resetHighlight();
      panel().innerHTML =
        '<p class="text-muted small mb-0">Нажмите на узел или ребро, чтобы увидеть детали</p>';
    }
  });

  // ── Legend ──────────────────────────────────────────────────────────────
  const legend = document.getElementById('ns-legend');
  legend.innerHTML = '';
  Object.entries(nsMap).forEach(([ns, color]) => {
    const el = document.createElement('span');
    el.className = 'small text-muted me-3';
    el.innerHTML = `<span class="ns-dot" style="background:${color}"></span>${esc(ns)}`;
    legend.appendChild(el);
  });
  const hint = document.createElement('span');
  hint.className = 'small text-muted ms-2';
  hint.innerHTML = '<span style="display:inline-block;width:0;height:0;border-left:6px solid transparent;border-right:6px solid transparent;border-bottom:10px solid #6c757d;vertical-align:middle;margin-right:4px"></span>Сервис';
  legend.appendChild(hint);
}

// ─── Evidence rendering ───────────────────────────────────────────────────
function renderEvidence(ev) {
  if (!ev || ev.length === 0) {
    return '<p class="text-muted small mb-0">Evidence отсутствует</p>';
  }
  return ev.map(e => {
    const isDefault = e.matchedBy === 'default-allow-noallow';
    return `
    <div class="evidence-item border rounded p-2 mb-2 bg-light">
      <span class="badge bg-secondary mb-1">${esc(matchLabel(e.matchedBy))}</span>
      ${isDefault
        ? `<div class="text-muted small mt-1">У получателя нет ALLOW-политик — Istio разрешает соединение по умолчанию.</div>`
        : `
          ${e.policy            ? `<div><span class="text-muted">Политика:</span> <code>${esc(e.policy)}</code></div>` : ''}
          ${e.sourceServiceAccount ? `<div><span class="text-muted">SA:</span> <code>${esc(e.sourceServiceAccount)}</code></div>` : ''}
          ${e.matchedValue      ? `<div><span class="text-muted">Значение:</span> <code>${esc(e.matchedValue)}</code></div>` : ''}
        `
      }
      ${e.service ? `<div><span class="text-muted">Сервис:</span> <code>${esc(e.service)}</code></div>` : ''}
    </div>`;
  }).join('');
}

function renderWorkloadInfo(wl, inbound, outbound) {
  const labelsHtml = wl.labels && Object.keys(wl.labels).length > 0
    ? Object.entries(wl.labels).map(([k, v]) =>
        `<span class="badge bg-light text-dark border me-1 mb-1" style="font-weight:400"><code>${esc(k)}: ${esc(v)}</code></span>`
      ).join('')
    : '<span class="text-muted small">нет</span>';

  const imagesHtml = wl.images && wl.images.length > 0
    ? wl.images.map(img => `<div class="small font-monospace text-break">${esc(img)}</div>`).join('')
    : '<span class="text-muted small">нет данных</span>';

  const svcNames = (wl.services || []).map(s => `<code class="small">${esc(s.name)}</code>`).join(', ')
    || '<span class="text-muted small">нет</span>';

  const inboundHtml = (inbound || []).length === 0
    ? '<p class="text-muted small mb-0">нет входящих разрешений</p>'
    : (inbound || []).map(e => `
        <div class="border-start border-3 border-primary ps-2 mb-2">
          <div class="small"><span class="text-muted">от</span> <code>${esc(e.source)}</code></div>
          <div class="small mb-1"><span class="text-muted">через</span> <code>${esc(e.viaService)}</code> :${e.port || '?'} <span class="text-muted">${esc(e.protocol || '')}</span></div>
          <div>${evidenceSummary(e.evidence)}</div>
        </div>`).join('');

  const outboundHtml = (outbound || []).length === 0
    ? '<p class="text-muted small mb-0">нет исходящих</p>'
    : (outbound || []).map(e => `
        <div class="border-start border-3 border-success ps-2 mb-2">
          <div class="small"><span class="text-muted">к</span> <code>${esc(e.dest)}</code></div>
          <div class="small"><span class="text-muted">через</span> <code>${esc(e.viaService)}</code> :${e.port || '?'}</div>
        </div>`).join('');

  return `
    <div class="fw-semibold mb-1">${esc(wl.name)}</div>
    <div class="mb-2"><span class="badge bg-dark">${esc(wl.kind || '?')}</span></div>
    <table class="table table-sm table-borderless mb-2" style="font-size:.85rem">
      <tr><td class="text-muted pe-2" style="white-space:nowrap">SA</td>
          <td><code>${esc(wl.serviceAccount)}</code></td></tr>
      <tr><td class="text-muted pe-2">Сервисы</td>
          <td>${svcNames}</td></tr>
    </table>
    <div class="text-muted small fw-semibold mb-1">Labels пода</div>
    <div class="mb-2">${labelsHtml}</div>
    <div class="text-muted small fw-semibold mb-1">Образы</div>
    <div class="mb-3">${imagesHtml}</div>
    <div class="text-muted small fw-semibold mb-1">
      <span class="badge bg-primary me-1">${(inbound || []).length}</span>Входящие разрешения
    </div>
    <div class="mb-3">${inboundHtml}</div>
    <div class="text-muted small fw-semibold mb-1">
      <span class="badge bg-success me-1">${(outbound || []).length}</span>Исходящие
    </div>
    <div>${outboundHtml}</div>
  `;
}

function renderServiceInfo(svc) {
  const portsHtml = svc.ports && svc.ports.length > 0
    ? svc.ports.map(p => `
        <div class="evidence-item border rounded p-2 mb-1 bg-light small">
          <span class="badge bg-secondary">${esc(p.protocol)}</span>
          <strong>:${p.port}</strong>
          ${p.targetPort ? `→ <code>${esc(p.targetPort)}</code>` : ''}
          ${p.name ? `<span class="text-muted ms-1">(${esc(p.name)})</span>` : ''}
        </div>`).join('')
    : '<span class="text-muted small">портов нет</span>';

  const selectorHtml = svc.selector && Object.keys(svc.selector).length > 0
    ? Object.entries(svc.selector).map(([k, v]) =>
        `<span class="badge bg-light text-dark border me-1 mb-1" style="font-weight:400"><code>${esc(k)}: ${esc(v)}</code></span>`
      ).join('')
    : '<span class="text-muted small">нет (selectorless)</span>';

  return `
    <div class="fw-semibold mb-1">
      ▲ ${esc(svc.name)}
    </div>
    <div class="mb-2">
      <span class="badge bg-secondary">${esc(svc.type || 'ClusterIP')}</span>
    </div>
    <div class="text-muted small fw-semibold mb-1">Порты</div>
    <div class="mb-2">${portsHtml}</div>
    <div class="text-muted small fw-semibold mb-1">Selector (матчит labels пода)</div>
    <div>${selectorHtml}</div>
  `;
}

function renderEdgeInfo(viaService, connEdges) {
  if (!connEdges || connEdges.length === 0) {
    return '<p class="text-muted small mb-0">Нет данных о соединении</p>';
  }
  const flowHtml = connEdges.map(e => `
    <div class="border rounded p-2 mb-2 bg-light">
      <div class="small mb-1">
        <code>${esc(e.source)}</code>
        <span class="text-muted mx-1">→</span>
        <code>${esc(e.dest)}</code>
        <span class="text-muted ms-2">:${e.port || '?'} ${esc(e.protocol || '')}</span>
      </div>
      ${renderEvidence(e.evidence)}
    </div>`).join('');

  return `
    <div class="fw-semibold mb-2">▲ ${esc(viaService)}</div>
    ${flowHtml}
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
  return types.map(t => `<span class="badge bg-secondary me-1 small">${esc(matchLabel(t))}</span>`).join('');
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
