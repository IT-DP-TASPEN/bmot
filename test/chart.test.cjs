const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function chartOptions(values, type = 'line') {
  let options;
  const el = { dataset: { type, series: JSON.stringify([{ Name: 'Aset', Points: values.map((Value, i) => ({ Label: String(i), Value })) }]) } };
  const echarts = { getInstanceByDom: () => null, init: () => ({ setOption: value => { options = value; }, resize: () => {} }) };
  const document = {
    querySelectorAll: selector => selector === '.chart' ? [el] : [],
    querySelector: () => null,
    getElementById: () => null,
    addEventListener: (name, callback) => { if (name === 'DOMContentLoaded') callback(); },
    body: { addEventListener: () => {} }
  };
  vm.runInNewContext(fs.readFileSync('web/static/js/app.js', 'utf8'), {
    document, window: { echarts, addEventListener: () => {} }, echarts, ResizeObserver: class { observe() {} }, URL
  });
  return options.yAxis;
}

test('line charts leave room around near-flat values without requiring zero', () => {
  const axis = chartOptions([100_000_000, 100_200_000]);
  assert.ok(axis.min > 0 && axis.min < 100_000_000);
  assert.ok(axis.max > 100_200_000);
  assert.ok(axis.max - axis.min >= 100_200_000 * 0.25);
  assert.ok(200_000 / (axis.max - axis.min) < 0.01);
  const wide = chartOptions([90_000_000, 110_000_000]);
  assert.ok(wide.max - wide.min >= 28_000_000);
  const nearZero = chartOptions([0, 10]);
  assert.equal(nearZero.min, 0);
  assert.ok(nearZero.max >= 14);
  const bar = chartOptions([100_000_000, 100_200_000], 'bar');
  assert.equal(bar.min, undefined);
  assert.equal(bar.scale, false);
});
