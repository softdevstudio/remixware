'use strict';

// Pads str on the left with ch until it is len characters long.
function leftPad(str, len, ch) {
  str = String(str);
  if (ch === undefined || ch === null || ch === '') {
    ch = ' ';
  } else {
    ch = String(ch);
  }
  let pad = len - str.length;
  if (pad <= 0) {
    return str;
  }
  let out = '';
  while (pad > 0) {
    if (pad & 1) {
      out += ch;
    }
    pad >>= 1;
    if (pad) {
      ch += ch;
    }
  }
  return out + str;
}

module.exports = leftPad;
