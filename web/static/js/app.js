(() => {
  const rupiah = n => {
    const a = Math.abs(n);
    const suffix = a >= 1e12 ? [' T', 1e12] : a >= 1e9 ? [' M', 1e9] : a >= 1e6 ? [' Jt', 1e6] : ['', 1];
    return 'Rp ' + new Intl.NumberFormat('id-ID', { maximumFractionDigits: 1 }).format(n / suffix[1]) + suffix[0];
  };
  function renderCharts() {
    document.querySelectorAll('.chart').forEach(el => {
      if (!window.echarts) { el.textContent = 'Grafik tidak tersedia.'; return; }
      const series = JSON.parse(el.dataset.series || '[]');
      if (!series.length || !series[0].Points?.length) { el.textContent = 'Tidak ada data untuk periode dan cabang yang dipilih.'; return; }
      const chart = echarts.getInstanceByDom(el) || echarts.init(el);
      chart.setOption({ color: ['#245b8c', '#4b9b8b'], grid: { left: 15, right: 20, top: 38, bottom: 24, containLabel: true },
        legend: { top: 8, right: 15, textStyle: { color: '#738497', fontSize: 11 } },
        tooltip: { trigger: 'axis', valueFormatter: rupiah },
        xAxis: { type: 'category', boundaryGap: el.dataset.type === 'bar', data: series[0].Points.map(p => p.Label), axisLabel: { color: '#8090a0', fontSize: 10 }, axisLine: { lineStyle: { color: '#dfe7ee' } } },
        yAxis: { type: 'value', scale: el.dataset.type !== 'bar', axisLabel: { formatter: rupiah, color: '#8090a0', fontSize: 10 }, splitLine: { lineStyle: { color: '#edf1f5' } } },
        series: series.map(s => ({ name: s.Name, type: el.dataset.type || 'line', smooth: false, symbol: 'circle', symbolSize: 5, showSymbol: false, lineStyle: { width: 2.5 }, areaStyle: el.dataset.type === 'bar' ? undefined : { opacity: .04 }, data: s.Points.map(p => p.Value) }))
      }, true);
      new ResizeObserver(() => chart.resize()).observe(el);
    });
  }
  function syncFilter() {
    const category = document.querySelector('.global-filter input[name=category]');
    const selected = document.querySelector('.tabs a.selected');
    if (category && selected) category.value = new URL(selected.href).searchParams.get('category') || category.value;
    const ctx = document.querySelector('.topbar-context strong');
    if (ctx) { const label = document.querySelector('.page-head')?.dataset.reportingDate; if (label) ctx.textContent = 'Posisi ' + label; }
  }
  function init() {
    renderCharts(); syncFilter();
    const mode = document.getElementById('period-mode');
    const input = document.getElementById('period-input');
    if (mode && input && !mode.dataset.bound) { mode.dataset.bound = 'true'; mode.addEventListener('change', () => {
      const value = input.value;
      input.type = mode.value === 'daily' ? 'date' : mode.value === 'monthly' ? 'month' : 'number';
      input.value = mode.value === 'daily' ? (value.length === 10 ? value : value.length === 7 ? value + '-01' : value + '-01-01') : mode.value === 'monthly' ? (value.length >= 7 ? value.slice(0, 7) : value + '-01') : value.slice(0, 4);
    }); }
    const menu = document.getElementById('menu-toggle');
    if (menu && !menu.dataset.bound) { menu.dataset.bound = 'true'; menu.addEventListener('click', () => { const open = document.getElementById('sidebar').classList.toggle('open'); menu.setAttribute('aria-expanded', String(open)); }); }
  }
  document.addEventListener('DOMContentLoaded', init);
  document.body.addEventListener('htmx:beforeSwap', () => document.querySelectorAll('#main .chart').forEach(el => window.echarts?.getInstanceByDom(el)?.dispose()));
  document.body.addEventListener('htmx:afterSwap', init);
  window.addEventListener('resize', () => document.querySelectorAll('.chart').forEach(el => window.echarts?.getInstanceByDom(el)?.resize()));
})();
