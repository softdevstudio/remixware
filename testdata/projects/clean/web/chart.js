// Draws the latency chart on the status page.

function scale(values, height) {
  const top = Math.max(...values, 1);
  return values.map((v) => Math.round((v / top) * height));
}

function drawBars(ctx, values, width, height) {
  const bars = scale(values, height);
  const step = width / Math.max(bars.length, 1);
  ctx.clearRect(0, 0, width, height);
  bars.forEach((h, i) => {
    ctx.fillStyle = values[i] > 500 ? '#c0392b' : '#2e7d4f';
    ctx.fillRect(i * step + 1, height - h, step - 2, h);
  });
}

function mergeOptions(defaults, given) {
  const out = Object.assign({}, defaults);
  for (const key of Object.keys(given || {})) {
    if (given[key] !== undefined) {
      out[key] = given[key];
    }
  }
  return out;
}

module.exports = { scale, drawBars, mergeOptions };
