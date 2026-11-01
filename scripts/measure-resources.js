// Measures RAM/CPU of every app process with the widget open and then with it closed
// (tray-only), for docs/EVOLUTION.md point 5. Cross-platform: it loads the built app
// (dist/main.js) in the same Electron process and samples app.getAppMetrics().
//
//   npm run build
//   env -u ELECTRON_RUN_AS_NODE npx electron scripts/measure-resources.js [openMinutes] [trayMinutes] [intervalSeconds]
//   (headless Linux: prefix with xvfb-run -a and add --no-sandbox)
//
// It uses a throw-away user data directory: no accounts, no network. Prints one CSV
// line per sample (phase,minute,processes,totalMB,totalCPU,breakdown) and a summary.
const { app, BrowserWindow } = require('electron');
const os = require('node:os');
const path = require('node:path');
const fs = require('node:fs');

const [openMin = 2, trayMin = 5, everySec = 15] = process.argv.slice(2).filter((a) => /^\d+(\.\d+)?$/.test(a)).map(Number);

app.setPath('userData', fs.mkdtempSync(path.join(os.tmpdir(), 'aipermiler-measure-')));
require(path.join(__dirname, '..', 'dist', 'main.js'));

const rows = { open: [], tray: [] };
let phase = 'open';
const startedAt = Date.now();
let phaseStart = startedAt;

function sample() {
  const metrics = app.getAppMetrics();
  const totalMB = metrics.reduce((s, m) => s + m.memory.workingSetSize, 0) / 1024;
  const cpu = metrics.reduce((s, m) => s + m.cpu.percentCPUUsage, 0);
  const minute = (Date.now() - phaseStart) / 60000;
  const parts = metrics.map((m) => `${m.type}${m.serviceName ? `:${m.serviceName}` : ''}=${Math.round(m.memory.workingSetSize / 1024)}MB/${m.cpu.percentCPUUsage.toFixed(1)}%`).join(' ');
  rows[phase].push({ totalMB, cpu, count: metrics.length });
  console.log(`${phase},${minute.toFixed(2)},${metrics.length},${totalMB.toFixed(0)},${cpu.toFixed(1)},${parts}`);
}

function summary(name) {
  // Skip the first samples: startup work (first refresh, window load) is not steady state.
  const r = rows[name].slice(Math.min(3, Math.floor(rows[name].length / 2)));
  if (r.length === 0) return;
  const avg = (k) => r.reduce((s, x) => s + x[k], 0) / r.length;
  console.log(`SUMMARY ${name}: processes=${r[r.length - 1].count} avgMB=${avg('totalMB').toFixed(0)} maxMB=${Math.max(...r.map((x) => x.totalMB)).toFixed(0)} avgCPU=${avg('cpu').toFixed(2)}% maxCPU=${Math.max(...r.map((x) => x.cpu)).toFixed(1)}% samples=${r.length}`);
}

app.whenReady().then(() => {
  const timer = setInterval(sample, everySec * 1000);
  setTimeout(() => {
    summary('open');
    phase = 'tray';
    phaseStart = Date.now();
    // Hide, like the widget close button (window:close in main.ts): the renderer stays alive.
    for (const w of BrowserWindow.getAllWindows()) w.hide();
    console.log(`# window hidden, windows visible: ${BrowserWindow.getAllWindows().filter((w) => w.isVisible()).length}`);
    setTimeout(() => {
      clearInterval(timer);
      summary('tray');
      app.exit(0);
    }, trayMin * 60000);
  }, openMin * 60000);
});
