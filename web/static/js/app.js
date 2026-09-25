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
      const values = series.flatMap(s => s.Points.map(p => p.Value));
      const low = Math.min(...values), high = Math.max(...values);
      const span = Math.max((high - low) * 1.4, Math.max(Math.abs(low), Math.abs(high)) * 0.25, 1);
      const center = (low + high) / 2;
      let axisMin = center - span / 2, axisMax = center + span / 2;
      if (low >= 0 && axisMin < 0) { axisMin = 0; axisMax = span; }
      if (high <= 0 && axisMax > 0) { axisMin = -span; axisMax = 0; }
      const chart = echarts.getInstanceByDom(el) || echarts.init(el);
      chart.setOption({ color: ['#003399', '#8a647a'], animation: false, textStyle: { fontFamily: '"Noto Sans", system-ui, sans-serif' },
        grid: { left: 12, right: 18, top: 36, bottom: 20, containLabel: true },
        legend: { top: 6, right: 12, itemWidth: 14, itemHeight: 3, textStyle: { color: '#484848', fontSize: 12 } },
        tooltip: { trigger: 'axis', valueFormatter: rupiah, backgroundColor: '#111111', borderWidth: 0, padding: [8, 12], textStyle: { color: '#ffffff', fontSize: 12 }, extraCssText: 'border-radius:4px;box-shadow:none' },
        xAxis: { type: 'category', boundaryGap: el.dataset.type === 'bar', data: series[0].Points.map(p => p.Label), axisTick: { show: false }, axisLabel: { color: '#767676', fontSize: 11 }, axisLine: { lineStyle: { color: '#dfdfdf' } } },
        yAxis: { type: 'value', scale: el.dataset.type !== 'bar', ...(el.dataset.type === 'bar' ? {} : { min: axisMin, max: axisMax }), axisLabel: { formatter: rupiah, color: '#767676', fontSize: 11 }, axisLine: { show: false }, splitLine: { lineStyle: { color: '#eeeeee' } } },
        series: series.map(s => ({ name: s.Name, type: el.dataset.type || 'line', smooth: false, symbol: 'circle', symbolSize: 4, showSymbol: false, lineStyle: { width: 2 }, data: s.Points.map(p => p.Value) }))
      }, true);
      new ResizeObserver(() => chart.resize()).observe(el);
    });
  }
  function syncFilter() {
    const filter = document.querySelector('.global-filter');
    const category = filter?.querySelector('input[name=category]');
    const selected = document.querySelector('.tabs a.selected');
    if (category && selected) category.value = new URL(selected.href).searchParams.get('category') || category.value;
    if (filter) document.querySelectorAll('.sidebar .brand, .sidebar nav a').forEach(link => {
      const url = new URL(link.href);
      for (const name of ['mode', 'period']) url.searchParams.set(name, filter.elements.namedItem(name).value);
      const userBranch = document.getElementById('sidebar').dataset.userBranch;
      url.searchParams.set('branch', userBranch === 'ALL' || url.pathname === '/kinerja' ? filter.elements.namedItem('branch').value : userBranch);
      link.href = url.href;
    });
  }
  function init() {
    renderCharts(); syncFilter();
    const heading = document.querySelector('#main .page-head h1');
    if (heading?.textContent.trim()) document.title = heading.textContent.trim() + ' | BM Monitoring';
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
