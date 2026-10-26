'use strict';

/**
 * Returns a function that runs fn once calls have stopped for wait ms.
 */
function debounce(fn, wait, immediate) {
  let timer = null;
  let lastArgs = null;
  function later() {
    timer = null;
    if (!immediate && lastArgs) {
      fn.apply(null, lastArgs);
      lastArgs = null;
    }
  }
  return function debounced(...args) {
    const callNow = immediate && timer === null;
    lastArgs = args;
    if (timer !== null) {
      clearTimeout(timer);
    }
    timer = setTimeout(later, wait);
    if (callNow) {
      fn.apply(null, args);
      lastArgs = null;
    }
  };
}

/**
 * Merges plain objects deeply. Arrays are replaced, not merged.
 */
function deepMerge(target, ...sources) {
  for (const source of sources) {
    if (source === null || typeof source !== 'object') {
      continue;
    }
    for (const key of Object.keys(source)) {
      const value = source[key];
      if (value && typeof value === 'object' && !Array.isArray(value)) {
        if (!target[key] || typeof target[key] !== 'object') {
          target[key] = {};
        }
        deepMerge(target[key], value);
      } else {
        target[key] = value;
      }
    }
  }
  return target;
}

module.exports = { debounce, deepMerge };
