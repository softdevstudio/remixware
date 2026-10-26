// Small helpers for the storefront pages.

function debounce(callback, delay, leading) {
  let handle = null;
  let pending = null;
  function fire() {
    handle = null;
    if (!leading && pending) {
      callback.apply(null, pending);
      pending = null;
    }
  }
  return function wrapped(...params) {
    const runNow = leading && handle === null;
    pending = params;
    if (handle !== null) {
      clearTimeout(handle);
    }
    handle = setTimeout(fire, delay);
    if (runNow) {
      callback.apply(null, params);
      pending = null;
    }
  };
}

function formatPrice(cents, currency) {
  const sign = cents < 0 ? '-' : '';
  const abs = Math.abs(cents);
  return sign + currency + Math.floor(abs / 100) + '.' + String(abs % 100).padStart(2, '0');
}

module.exports = { debounce, formatPrice };
